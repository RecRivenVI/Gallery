// Package sourcelab 是真实只读 Source 的验证 orchestrator：它消费
// testlabrulesimport 产出的**转换产物**（而不是手写规则夹具），把每一次触碰真实
// Source 的操作夹在 sourceguard 的前后校验之间，在显式边界内执行 index/incremental/
// verify 档案，并只输出脱敏计数。
//
// 三条与「证据是否成立」直接相关的设计：
//
//   - 规则来自 internal/rules/legacy.Convert 的产物。手写夹具与用户真实配置之间没有
//     任何同步机制，用它验证只能证明夹具自洽。
//   - guard 不是可选步骤。此前每个场景各自记得调用两次 Walk，漏掉的那一步在报告里看
//     不出区别；这里所有真实 Source 操作都必须经 Guard.Around 执行。
//   - 「跑完了」与「撞到边界停下了」是两个结论。任何一次触顶都会带着 Reason 进入报告，
//     不允许把有界运行说成完整覆盖。
//
// 本包只导入 pkg/galleryapi 与 tools/testlab 的共享模块，不导入 internal/*，也不直接
// 读写数据库。
package sourcelab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
)

// 运行模式。
const (
	// ModeBounded 在显式目录/文件/墙钟上限内运行 index 档案，用于第一次接触一个尚未
	// 摸清规模的来源。
	ModeBounded = "bounded"
	// ModeIndex 完整枚举 + metadata 解析 + publication，并按存储介质决定内容哈希范围。
	ModeIndex = "index"
	// ModeIncremental 证明续跑不重做已完成工作。
	ModeIncremental = "incremental"
	// ModeVerify 强制重新完整哈希，作为 incremental 复用语义的对照组。
	ModeVerify = "verify"
)

// 内容哈希范围。
const (
	// HashFull 让生产扫描器对全部媒体计算完整 SHA-256（SSD 平台）。
	HashFull = "full"
	// HashBounded 只对有界子集做按需确认（HDD 平台）。
	HashBounded = "bounded"
)

// Config 是一次真实 Source 验证运行的完整输入。
type Config struct {
	// Entry 来自转换产物索引；SourceRoot 是真实只读根，只用于登记 Source，绝不进报告。
	Entry ruleindex.Entry
	// Package 是该平台由 legacy.Convert 产出的规则包。
	Package map[string]any
	Mode    string
	Limits  bounds.Limits
	// HashScope 是 HashFull 或 HashBounded。
	HashScope string
	// MaxMediaItems 是 HashBounded 下按需确认的媒体数上限，同时也是逐媒体抽样的上限。
	MaxMediaItems int
	// GuardOptions 控制 guard 清单的证据强度（内容哈希默认关闭）。
	GuardOptions sourceguard.Options
	// StorageClass 只作为报告标注（ssd/hdd），不影响任何判定。
	StorageClass string
}

// Validate 拒绝名不副实的配置。
func (c Config) Validate() error {
	switch c.Mode {
	case ModeBounded, ModeIndex, ModeIncremental, ModeVerify:
	default:
		return fmt.Errorf("未知运行模式 %q", c.Mode)
	}
	if c.Entry.PlatformCode == "" || c.Entry.SourceRoot == "" {
		return fmt.Errorf("转换产物条目缺少平台代号或只读根")
	}
	if len(c.Package) == 0 {
		return fmt.Errorf("缺少规则包内容")
	}
	if c.Mode == ModeBounded && c.Limits.Unlimited() {
		return fmt.Errorf("bounded 模式必须至少设置一项边界（目录数/文件数/墙钟）；全不设限的「有界模式」名不副实")
	}
	switch c.HashScope {
	case HashFull, HashBounded, "":
	default:
		return fmt.Errorf("未知内容哈希范围 %q", c.HashScope)
	}
	return nil
}

// State 是可跨进程续跑的运行状态：让第二次运行知道第一次做到了哪里，而不是重头再来。
type State struct {
	PlatformCode string `json:"platformCode"`
	LibraryID    string `json:"libraryId"`
	SourceID     string `json:"sourceId"`
	// BindingID 是本验证持有的 SourceRuleBinding。真实大 Source 的完整 guard 可能持续
	// 数分钟；Binding 若在这段时间保持 active，30 秒 Watcher 会先创建系统扫描，使随后
	// 的显式档案请求稳定命中 SCAN_ALREADY_RUNNING。sourcelab 因此只在创建显式 Job 的
	// 极短窗口恢复 Binding，Job 持久化后立即重新暂停；执行仍使用 Job 冻结的规则快照。
	BindingID string `json:"bindingId"`
	// LastProfile 是最近一次成功完成的扫描档案。
	LastProfile string `json:"lastScanProfile"`
	// WorkCount/MediaCount/VerifiedCount 是最近一次的发布规模。
	WorkCount     int `json:"workCount"`
	MediaCount    int `json:"mediaCount"`
	VerifiedCount int `json:"verifiedCount"`
	// DigestFold 是全部已确认媒体摘要的折叠哈希；身份是否漂移看它。
	DigestFold string `json:"digestFold"`
	// VerifiedAtFold 是全部已确认媒体确认时间的折叠哈希；是否重做过哈希看它。
	VerifiedAtFold string `json:"verifiedAtFold"`
	UpdatedAt      string `json:"updatedAt"`
}

// Run 执行一次运行，并把全部结论写进 rep。返回错误表示本次运行结论不成立
// （调用方必须以非零退出码结束）。
func Run(rep *report.Report, sess *environment.Session, cfg Config, previous *State) (*State, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	rep.SourceAlias = cfg.Entry.PlatformCode
	if cfg.StorageClass != "" {
		rep.StorageClass = cfg.StorageClass
	}

	census, err := sourceguard.TakeCensus(cfg.Entry.SourceRoot, cfg.Limits)
	if err != nil {
		rep.Add("sourcelab/census", false, sanitize(err.Error()))
		return nil, err
	}
	rep.Add("sourcelab/census", true, fmt.Sprintf("%s links=%d topLevelDirs=%d maxDepth=%d bytes=%d",
		census.Outcome.Summary(), census.Links, census.TopLevelDirs, census.MaxDepth, census.TotalBytes))
	if !census.Outcome.Completed {
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("枚举因边界停止（%s），本次覆盖的是该来源的一个子集，不代表完整来源", census.Outcome.Reason))
	}

	guard, err := sourceguard.NewGuard(cfg.Entry.SourceRoot, cfg.Entry.PlatformCode, cfg.GuardOptions)
	if err != nil {
		rep.Add("sourcelab/guard-baseline", false, sanitize(err.Error()))
		return nil, err
	}
	baseline := guard.Baseline()
	rep.Add("sourcelab/guard-baseline", true, fmt.Sprintf("files=%d dirs=%d links=%d bytes=%d hashedFiles=%d hashStoppedByBound=%q",
		baseline.FileCount, baseline.DirCount, baseline.LinkCount, baseline.TotalBytes,
		baseline.HashedFileCount, baseline.HashStopReason))
	if baseline.HashStopReason != "" {
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("guard 内容哈希因边界停止（%s），只对已哈希子集覆盖「同大小同 mtime 原地改写」检测", baseline.HashStopReason))
	}

	state := &State{PlatformCode: cfg.Entry.PlatformCode}
	if previous != nil && previous.PlatformCode == cfg.Entry.PlatformCode {
		state.LibraryID, state.SourceID, state.BindingID = previous.LibraryID, previous.SourceID, previous.BindingID
	}

	if err := ensureRegistration(rep, sess, cfg, guard, state); err != nil {
		return state, err
	}

	var runErr error
	switch cfg.Mode {
	case ModeBounded:
		runErr = runBounded(rep, sess, cfg, guard, state)
	case ModeIndex:
		runErr = runIndex(rep, sess, cfg, guard, state)
	case ModeIncremental:
		runErr = runIncremental(rep, sess, cfg, guard, state, previous)
	case ModeVerify:
		runErr = runVerify(rep, sess, cfg, guard, state, previous)
	}

	// 无论本轮场景成败，都必须给出一次收尾 guard 结论：失败路径同样可能已经写过 Source。
	final, guardErr := guard.Verify("run-final")
	rep.Add("sourcelab/guard-final-unchanged", final.OK, final.Summary())
	if runErr != nil {
		return state, runErr
	}
	if guardErr != nil {
		return state, guardErr
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return state, nil
}

// ensureRegistration 建立或复用 Library/Source/RuleVersion/Binding。
//
// 复用是续跑的基础：同一个 AppDirs 上第二次运行必须接着上次的 Source 继续，而不是
// 再登记一个新的、内容完全相同的 Source（那会让「没有重做」无从谈起）。
func ensureRegistration(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State) error {
	ctx := context.Background()
	libraryName := "testlab-" + cfg.Entry.PlatformCode
	sourceName := cfg.Entry.PlatformCode

	libraries, err := sess.Client.ListLibrariesWithResponse(ctx, sess.SameOrigin)
	if err != nil || libraries.JSON200 == nil {
		rep.Add("sourcelab/registration", false, fmt.Sprintf("列出 Library 失败 status=%d", environment.StatusOf(libraries)))
		return fmt.Errorf("列出 Library 失败: %w", err)
	}
	for _, library := range libraries.JSON200.Libraries {
		if library.Name == libraryName {
			state.LibraryID = library.Id
			break
		}
	}
	if state.LibraryID == "" {
		created, err := sess.Client.CreateLibraryWithResponse(ctx, &api.CreateLibraryParams{XGalleryCSRF: sess.CSRF},
			api.LibraryCreateRequest{Name: libraryName}, sess.SameOrigin)
		if err != nil || created.JSON201 == nil {
			rep.Add("sourcelab/registration", false, fmt.Sprintf("创建 Library 失败 status=%d", environment.StatusOf(created)))
			return fmt.Errorf("创建 Library 失败: %w", err)
		}
		state.LibraryID = created.JSON201.Id
	}

	sources, err := sess.Client.ListSourcesWithResponse(ctx, &api.ListSourcesParams{LibraryId: &state.LibraryID}, sess.SameOrigin)
	if err != nil || sources.JSON200 == nil {
		rep.Add("sourcelab/registration", false, fmt.Sprintf("列出 Source 失败 status=%d", environment.StatusOf(sources)))
		return fmt.Errorf("列出 Source 失败: %w", err)
	}
	reused := false
	for _, source := range sources.JSON200.Sources {
		if source.DisplayName == sourceName {
			state.SourceID = source.Id
			reused = true
			break
		}
	}

	if !reused {
		// 登记 Source 会让服务端 stat 真实根；夹在 guard 之间执行。
		if err := guard.Around("register-source", func() error {
			created, err := sess.Client.CreateSourceWithResponse(ctx, &api.CreateSourceParams{XGalleryCSRF: sess.CSRF},
				api.SourceCreateRequest{LibraryId: state.LibraryID, DisplayName: sourceName, RootPath: cfg.Entry.SourceRoot}, sess.SameOrigin)
			if err != nil || created.JSON201 == nil {
				return fmt.Errorf("创建 Source 失败 status=%d", environment.StatusOf(created))
			}
			state.SourceID = created.JSON201.Id
			return nil
		}); err != nil {
			rep.Add("sourcelab/registration", false, sanitize(err.Error()))
			return err
		}
	}

	// 规则包内容确定（rule_set_id 由平台 ID 确定性派生），因此重复提交同一份包会命中
	// 已有 RuleVersion；这里不区分「新建」与「命中已有」，两者都是可接受结果。
	ruleResp, err := sess.Client.CreateRuleVersionWithResponse(ctx, &api.CreateRuleVersionParams{XGalleryCSRF: sess.CSRF},
		api.RuleVersionCreateRequest{Package: cfg.Package}, sess.SameOrigin)
	semanticHash := ""
	switch {
	case err != nil:
		rep.Add("sourcelab/rule-version", false, sanitize(err.Error()))
		return fmt.Errorf("提交转换产出的 RuleVersion 失败: %w", err)
	case ruleResp.JSON201 != nil:
		semanticHash = ruleResp.JSON201.SemanticHash
	default:
		rep.Add("sourcelab/rule-version", false, fmt.Sprintf("status=%d", environment.StatusOf(ruleResp)))
		return fmt.Errorf("提交转换产出的 RuleVersion 被拒绝: status=%d", environment.StatusOf(ruleResp))
	}
	rep.Add("sourcelab/rule-version", true, fmt.Sprintf("primitives=%d semanticHashLen=%d", cfg.Entry.PrimitiveCount, len(semanticHash)))

	bindings, err := sess.Client.ListSourceRuleBindingsWithResponse(ctx, &api.ListSourceRuleBindingsParams{SourceId: &state.SourceID}, sess.SameOrigin)
	if err != nil || bindings.JSON200 == nil {
		detail := fmt.Sprintf("status=%d", environment.StatusOf(bindings))
		if err != nil {
			detail = sanitize(err.Error())
		}
		rep.Add("sourcelab/registration", false, detail)
		return fmt.Errorf("读取 SourceRuleBinding 失败: %s", detail)
	}
	bound := false
	previousBindingID := state.BindingID
	for _, binding := range bindings.JSON200.Bindings {
		if string(binding.SemanticHash) != semanticHash {
			continue
		}
		state.BindingID = string(binding.Id)
		bound = true
		if state.BindingID == previousBindingID {
			break
		}
	}
	if !bound && len(bindings.JSON200.Bindings) > 0 {
		detail := "已有 Binding 的 semantic hash 与本次转换结果不一致；请使用新的隔离 AppDirs"
		rep.Add("sourcelab/registration", false, detail)
		return fmt.Errorf("%s", detail)
	}
	if !bound {
		created, err := sess.Client.CreateSourceRuleBindingWithResponse(ctx, &api.CreateSourceRuleBindingParams{XGalleryCSRF: sess.CSRF},
			api.NewDirectSourceRuleBindingCreateRequest(state.SourceID, semanticHash, map[string]any{}, 0),
			sess.SameOrigin)
		if err != nil || created.JSON201 == nil {
			detail := fmt.Sprintf("status=%d", environment.StatusOf(created))
			if err != nil {
				detail = sanitize(err.Error())
			}
			rep.Add("sourcelab/registration", false, detail)
			return fmt.Errorf("绑定转换产出的规则失败: %s", detail)
		}
		state.BindingID = string(created.JSON201.Id)
	}
	// Source 注册后，Watcher 的首次 online 收敛会把它标为 dirty。对真实大 Source，
	// 后续 scan 前 guard 足以跨过多个 30 秒周期；这里先暂停 Binding，避免 Watcher 抢在
	// 显式 index/incremental/verify 之前创建默认扫描。runScan 会在 Job 创建窗口恢复它。
	if err := setBindingStatus(sess, state.BindingID, api.UpdateSourceRuleBindingJSONBodyStatusPaused); err != nil {
		rep.Add("sourcelab/registration", false, sanitize(err.Error()))
		return err
	}
	rep.Add("sourcelab/registration", true, fmt.Sprintf("reusedSource=%v reusedBinding=%v", reused, bound))
	return nil
}

func setBindingStatus(sess *environment.Session, bindingID string, status api.UpdateSourceRuleBindingJSONBodyStatus) error {
	if strings.TrimSpace(bindingID) == "" {
		return fmt.Errorf("缺少 sourcelab SourceRuleBinding ID")
	}
	response, err := sess.Client.UpdateSourceRuleBindingWithResponse(context.Background(), api.SourceRuleBindingId(bindingID),
		&api.UpdateSourceRuleBindingParams{XGalleryCSRF: sess.CSRF},
		api.UpdateSourceRuleBindingJSONRequestBody{Status: status}, sess.SameOrigin)
	if err != nil {
		return fmt.Errorf("更新 sourcelab SourceRuleBinding 状态失败: %w", err)
	}
	if response.JSON200 == nil {
		return fmt.Errorf("更新 sourcelab SourceRuleBinding 状态失败: status=%d", environment.StatusOf(response))
	}
	return nil
}

// scanOutcome 是一次扫描的脱敏结论。
type scanOutcome struct {
	Profile     string
	Status      string
	Cancelled   bool
	StoppedBy   string
	Elapsed     time.Duration
	IssueCode   string
	Publication string
}

func (o scanOutcome) summary() string {
	return fmt.Sprintf("profile=%s status=%s cancelled=%v stoppedByBound=%q elapsedMs=%d issue=%q",
		o.Profile, o.Status, o.Cancelled, o.StoppedBy, o.Elapsed.Milliseconds(), o.IssueCode)
}

// runScan 触发一次扫描并在墙钟边界内等待。
//
// 超过墙钟上限时**主动取消**而不是继续等：这既是「有界」的真实含义，也顺带给出一次
// 真实的中断，使随后的 incremental 能证明「续跑不重做已完成工作」。
func runScan(sess *environment.Session, sourceID, bindingID, profile string, wallClock time.Duration) (scanOutcome, error) {
	ctx := context.Background()
	outcome := scanOutcome{Profile: profile}
	started := time.Now()
	if err := setBindingStatus(sess, bindingID, api.UpdateSourceRuleBindingJSONBodyStatusActive); err != nil {
		return outcome, err
	}

	body := api.ScanJobCreateRequest{}
	if profile != "" {
		value := api.ScanJobCreateRequestScanProfile(profile)
		body.ScanProfile = &value
	}
	created, err := sess.Client.CreateScanJobWithResponse(ctx, sourceID, &api.CreateScanJobParams{XGalleryCSRF: sess.CSRF}, body, sess.SameOrigin)
	// Job 已冻结 semantic hash/IR/参数；立即暂停 Binding 只阻止 Watcher 在当前扫描期间或
	// 完成后再创建默认扫描，不改变这个 Job 的执行语义。创建失败时也必须恢复 paused。
	pauseErr := setBindingStatus(sess, bindingID, api.UpdateSourceRuleBindingJSONBodyStatusPaused)
	if err != nil || created.JSON202 == nil {
		if pauseErr != nil {
			return outcome, pauseErr
		}
		return outcome, fmt.Errorf("创建 %s 扫描失败: status=%d", profile, environment.StatusOf(created))
	}
	if pauseErr != nil {
		return outcome, pauseErr
	}
	jobID := created.JSON202.Id

	var deadline time.Time
	if wallClock > 0 {
		deadline = started.Add(wallClock)
	}
	cancelRequested := false
	for {
		snapshot, err := sess.Client.GetJobWithResponse(ctx, jobID, sess.SameOrigin)
		if err != nil || snapshot.JSON200 == nil {
			return outcome, fmt.Errorf("读取 Job 快照失败: status=%d", environment.StatusOf(snapshot))
		}
		job := snapshot.JSON200
		status := string(job.Status)
		if status == "completed" || status == "failed" || status == "cancelled" || status == "needs_repair" {
			outcome.Status = status
			outcome.Elapsed = time.Since(started)
			if job.IssueCode != nil {
				outcome.IssueCode = *job.IssueCode
			}
			if job.QueryPublicationId != nil {
				outcome.Publication = *job.QueryPublicationId
			}
			return outcome, nil
		}
		if !cancelRequested && !deadline.IsZero() && !time.Now().Before(deadline) {
			cancelRequested = true
			outcome.Cancelled = true
			outcome.StoppedBy = bounds.ReasonWallClock
			if _, err := sess.Client.CancelJobWithResponse(ctx, jobID, &api.CancelJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin); err != nil {
				return outcome, fmt.Errorf("墙钟边界到达后取消扫描失败: %w", err)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// mediaSummary 是一次媒体抽样的脱敏汇总。
type mediaSummary struct {
	WorkCount       int
	MediaCount      int
	VerifiedCount   int
	UnverifiedCount int
	WithCreator     int
	WithPublishedAt int
	WithBadges      int
	WithCover       int
	TotalBytes      int64
	DigestFold      string
	VerifiedAtFold  string
	Outcome         bounds.Outcome
}

func (s mediaSummary) summary() string {
	return fmt.Sprintf("works=%d media=%d verified=%d unverified=%d creator=%d publishedAt=%d badges=%d cover=%d bytes=%d digestFold=%s",
		s.WorkCount, s.MediaCount, s.VerifiedCount, s.UnverifiedCount, s.WithCreator, s.WithPublishedAt,
		s.WithBadges, s.WithCover, s.TotalBytes, shortFold(s.DigestFold))
}

func shortFold(fold string) string {
	if len(fold) <= 16 {
		return fold
	}
	return fold[:16]
}

// collectMedia 按边界枚举 publication 中的作品与媒体，只累加计数与折叠哈希。
//
// 折叠哈希（而不是逐条摘要）是刻意的：它足以回答「两次运行之间内容身份/确认时间有没有
// 变」，又不会把逐媒体信息带进可能被展示的报告。
func collectMedia(sess *environment.Session, libraryID string, limits bounds.Limits, maxMediaItems int) (mediaSummary, error) {
	ctx := context.Background()
	summary := mediaSummary{}
	budget := limits.Start(time.Now)
	started := time.Now()

	digestParts := []string{}
	verifiedParts := []string{}
	var cursor *string
	const pageSize = 100

	for {
		if !budget.CheckWallClock() {
			break
		}
		params := api.ListWorksParams{LibraryId: &libraryID, Limit: intPtr(pageSize), Cursor: cursor}
		listed, err := sess.ListWorks(params)
		if err == nil && listed.JSON200 == nil && environment.StatusOf(listed) == 404 {
			// 尚无任何 publication：这是「还没索引过」的正常状态，不是失败。
			break
		}
		if err != nil || listed.JSON200 == nil {
			return summary, fmt.Errorf("列出作品失败: status=%d", environment.StatusOf(listed))
		}
		for _, work := range listed.JSON200.Works {
			if !budget.AddDir() {
				break
			}
			summary.WorkCount++
			if strings.TrimSpace(work.Creator) != "" {
				summary.WithCreator++
			}
			if work.PublishedAt != nil {
				summary.WithPublishedAt++
			}
			if len(work.Badges) > 0 {
				summary.WithBadges++
			}
			if work.CoverMediaId != nil {
				summary.WithCover++
			}
			if maxMediaItems > 0 && summary.MediaCount >= maxMediaItems {
				continue
			}
			mediaResp, err := sess.Client.ListWorkMediaWithResponse(ctx, work.Id, &api.ListWorkMediaParams{}, sess.SameOrigin)
			if err != nil || mediaResp.JSON200 == nil {
				return summary, fmt.Errorf("列出作品媒体失败: status=%d", environment.StatusOf(mediaResp))
			}
			for _, item := range mediaResp.JSON200.Media {
				if !budget.AddFile() {
					break
				}
				summary.MediaCount++
				summary.TotalBytes += item.SizeBytes
				if string(item.ContentVerificationState) == "content_verified" {
					summary.VerifiedCount++
					if item.Blob != nil {
						digestParts = append(digestParts, item.Id+"|"+item.Blob.Digest)
					}
					if item.VerifiedAt != nil {
						verifiedParts = append(verifiedParts, item.Id+"|"+item.VerifiedAt.UTC().Format(time.RFC3339Nano))
					}
				} else {
					summary.UnverifiedCount++
				}
			}
		}
		if budget.Stopped() || listed.JSON200.NextCursor == nil || *listed.JSON200.NextCursor == "" {
			break
		}
		cursor = listed.JSON200.NextCursor
	}

	summary.DigestFold = fold(digestParts)
	summary.VerifiedAtFold = fold(verifiedParts)
	summary.Outcome = budget.Outcome(time.Since(started))
	return summary, nil
}

func fold(parts []string) string {
	sort.Strings(parts)
	hasher := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(hasher, "%s\n", part)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func intPtr(v int) *int { return &v }

// indexIfNeeded 只在该 Source 尚未建立过 publication 时执行 index 档案。
//
// 这不是优化而是产品语义：`index` 档案只对首次扫描有效，Source 已发布或已有持久历史后
// 显式请求 index 会被服务端稳定拒绝为 CONFLICT。因此「已经索引过」是一个正常的续跑状态，
// 必须如实报告为「复用上次结果」，而不是把 409 当成失败，也不是偷偷改跑 incremental。
func indexIfNeeded(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard,
	state *State, findingPrefix string) (scanOutcome, bool, error) {
	existing, err := collectMedia(sess, state.LibraryID, bounds.Limits{MaxDirs: 1, MaxFiles: 1}, 1)
	if err != nil {
		rep.Add(findingPrefix+"-precheck", false, sanitize(err.Error()))
		return scanOutcome{}, false, err
	}
	if existing.WorkCount > 0 {
		rep.Add(findingPrefix+"-reused-existing-index", true,
			fmt.Sprintf("该 Source 已有 publication，index 档案不再适用，复用既有索引结果 previousProfile=%q", state.LastProfile))
		return scanOutcome{Profile: "index", Status: "reused"}, true, nil
	}
	var outcome scanOutcome
	err = guard.Around("scan-index", func() error {
		var scanErr error
		outcome, scanErr = runScan(sess, state.SourceID, state.BindingID, "index", cfg.Limits.MaxWallClock)
		return scanErr
	})
	return outcome, false, err
}

// runBounded 在显式边界内执行 index 档案，并对有界子集做按需确认。
func runBounded(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State) error {
	outcome, reused, err := indexIfNeeded(rep, sess, cfg, guard, state, "sourcelab/bounded-index")
	if err != nil {
		rep.Add("sourcelab/bounded-index-scan", false, sanitize(err.Error()))
		return err
	}
	if !reused {
		rep.Add("sourcelab/bounded-index-scan", outcome.Status == "completed" || outcome.Cancelled, outcome.summary())
	}
	if outcome.Cancelled {
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("index 扫描因墙钟边界（%s）被主动取消，本次结论只覆盖已完成部分", cfg.Limits.MaxWallClock))
		// 被取消的扫描不发布 publication，后续断言无从谈起；如实报告并结束。
		rep.Add("sourcelab/bounded-stopped-by-bound", true, outcome.summary())
		return nil
	}
	if outcome.Status != "completed" {
		return fmt.Errorf("bounded index 扫描未完成: %s", outcome.summary())
	}

	summary, err := collectMedia(sess, state.LibraryID, cfg.Limits, cfg.MaxMediaItems)
	if err != nil {
		rep.Add("sourcelab/bounded-projection", false, sanitize(err.Error()))
		return err
	}
	rep.Add("sourcelab/bounded-projection", summary.WorkCount > 0 && summary.MediaCount > 0,
		summary.summary()+" "+summary.Outcome.Summary())
	if !reused {
		// 只有本轮真的跑了 index 档案才能断言「发布未确认媒体」：复用既有索引时，
		// 上一轮的按需确认结果本来就应该还在。
		rep.Add("sourcelab/bounded-index-publishes-unverified", summary.VerifiedCount == 0,
			fmt.Sprintf("verified=%d unverified=%d", summary.VerifiedCount, summary.UnverifiedCount))
	}

	confirmed, stoppedBy, err := confirmBoundedSubset(rep, sess, cfg, guard, state)
	if err != nil {
		return err
	}
	rep.Add("sourcelab/bounded-on-demand-verification", confirmed > 0,
		fmt.Sprintf("confirmed=%d limit=%d stoppedByBound=%q", confirmed, cfg.MaxMediaItems, stoppedBy))

	state.LastProfile = "index"
	state.WorkCount, state.MediaCount, state.VerifiedCount = summary.WorkCount, summary.MediaCount, confirmed
	state.DigestFold, state.VerifiedAtFold = summary.DigestFold, summary.VerifiedAtFold
	return nil
}

// runIndex 完整枚举 + metadata 解析 + publication，并按存储介质决定内容哈希范围。
func runIndex(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State) error {
	outcome, reused, err := indexIfNeeded(rep, sess, cfg, guard, state, "sourcelab/index")
	if err != nil {
		rep.Add("sourcelab/index-scan", false, sanitize(err.Error()))
		return err
	}
	if !reused {
		rep.Add("sourcelab/index-scan", outcome.Status == "completed" && !outcome.Cancelled, outcome.summary())
		if outcome.Cancelled {
			rep.Limitations = append(rep.Limitations, "index 扫描因墙钟边界被取消，本次不是全量 index")
			return fmt.Errorf("index 模式要求完整跑完，但扫描因边界被取消: %s", outcome.summary())
		}
		if outcome.Status != "completed" {
			return fmt.Errorf("index 扫描未完成: %s", outcome.summary())
		}
	}

	summary, err := collectMedia(sess, state.LibraryID, bounds.Limits{}, 0)
	if err != nil {
		rep.Add("sourcelab/index-projection", false, sanitize(err.Error()))
		return err
	}
	rep.Add("sourcelab/index-projection", summary.WorkCount > 0 && summary.MediaCount > 0, summary.summary())
	rep.Add("sourcelab/index-metadata-parsed", summary.WithCreator > 0,
		fmt.Sprintf("works=%d withCreator=%d withPublishedAt=%d withBadges=%d withCover=%d",
			summary.WorkCount, summary.WithCreator, summary.WithPublishedAt, summary.WithBadges, summary.WithCover))
	if !reused {
		rep.Add("sourcelab/index-publishes-unverified", summary.VerifiedCount == 0,
			fmt.Sprintf("verified=%d unverified=%d", summary.VerifiedCount, summary.UnverifiedCount))
	}

	state.LastProfile = "index"
	state.WorkCount, state.MediaCount = summary.WorkCount, summary.MediaCount

	switch cfg.HashScope {
	case HashFull:
		var hashOutcome scanOutcome
		err := guard.Around("scan-incremental-full-hash", func() error {
			var scanErr error
			hashOutcome, scanErr = runScan(sess, state.SourceID, state.BindingID, "incremental", cfg.Limits.MaxWallClock)
			return scanErr
		})
		if err != nil {
			rep.Add("sourcelab/full-content-hash", false, sanitize(err.Error()))
			return err
		}
		if hashOutcome.Status != "completed" || hashOutcome.Cancelled {
			rep.Add("sourcelab/full-content-hash", false, hashOutcome.summary())
			return fmt.Errorf("全量内容哈希未完成: %s", hashOutcome.summary())
		}
		hashed, err := collectMedia(sess, state.LibraryID, bounds.Limits{}, 0)
		if err != nil {
			rep.Add("sourcelab/full-content-hash", false, sanitize(err.Error()))
			return err
		}
		rep.Add("sourcelab/full-content-hash", hashed.VerifiedCount == hashed.MediaCount && hashed.MediaCount > 0,
			fmt.Sprintf("%s elapsedMs=%d", hashed.summary(), hashOutcome.Elapsed.Milliseconds()))
		state.LastProfile = "incremental"
		state.VerifiedCount = hashed.VerifiedCount
		state.DigestFold, state.VerifiedAtFold = hashed.DigestFold, hashed.VerifiedAtFold
	case HashBounded, "":
		confirmed, _, err := confirmBoundedSubset(rep, sess, cfg, guard, state)
		if err != nil {
			return err
		}
		bounded, err := collectMedia(sess, state.LibraryID, bounds.Limits{}, 0)
		if err != nil {
			rep.Add("sourcelab/bounded-content-hash", false, sanitize(err.Error()))
			return err
		}
		rep.Add("sourcelab/bounded-content-hash", confirmed > 0 && bounded.VerifiedCount == confirmed,
			fmt.Sprintf("confirmed=%d limit=%d verified=%d media=%d", confirmed, cfg.MaxMediaItems, bounded.VerifiedCount, bounded.MediaCount))
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("本次只对 %d 个媒体做了完整内容哈希（有界子集），未对全部媒体建立 ContentBlob", confirmed))
		state.VerifiedCount = bounded.VerifiedCount
		state.DigestFold, state.VerifiedAtFold = bounded.DigestFold, bounded.VerifiedAtFold
	}
	return nil
}

// confirmBoundedSubset 对前若干个未确认媒体做目标化按需确认。bounded 模式下，
// MaxWallClock 同时约束整个确认阶段，而不是让每个媒体分别获得 30 分钟：真实 Source
// 可能把一个超大附件排在很前面，逐媒体独立超时会让名义上的有界运行膨胀到数小时。
func confirmBoundedSubset(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State) (int, string, error) {
	ctx := context.Background()
	limit := cfg.MaxMediaItems
	if limit <= 0 {
		limit = 8
	}
	targets, err := unverifiedMediaIDs(sess, state.LibraryID, limit)
	if err != nil {
		rep.Add("sourcelab/on-demand-targets", false, sanitize(err.Error()))
		return 0, "", err
	}
	rep.Add("sourcelab/on-demand-targets", len(targets) > 0, fmt.Sprintf("targets=%d limit=%d", len(targets), limit))

	confirmed := 0
	stoppedBy := ""
	verificationDeadline := time.Time{}
	if cfg.Mode == ModeBounded && cfg.Limits.MaxWallClock > 0 {
		verificationDeadline = time.Now().Add(cfg.Limits.MaxWallClock)
	}
	err = guard.Around("on-demand-verification", func() error {
		for _, mediaID := range targets {
			waitTimeout := 30 * time.Minute
			cancelAtTimeout := false
			if !verificationDeadline.IsZero() {
				waitTimeout = time.Until(verificationDeadline)
				cancelAtTimeout = true
				if waitTimeout <= 0 {
					stoppedBy = bounds.ReasonWallClock
					break
				}
			}
			if err := setBindingStatus(sess, state.BindingID, api.UpdateSourceRuleBindingJSONBodyStatusActive); err != nil {
				return err
			}
			created, err := sess.Client.CreateMediaVerificationJobWithResponse(ctx, mediaID,
				&api.CreateMediaVerificationJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
			pauseErr := setBindingStatus(sess, state.BindingID, api.UpdateSourceRuleBindingJSONBodyStatusPaused)
			if err != nil {
				return fmt.Errorf("创建按需确认 Job 失败: %w", err)
			}
			if pauseErr != nil {
				return pauseErr
			}
			if created.JSON202 == nil {
				// 已确认或不适用：稳定拒绝，不是缺陷。
				continue
			}
			job, stopped, err := waitForJob(sess, created.JSON202.Id, waitTimeout, cancelAtTimeout)
			if err != nil {
				return err
			}
			if stopped {
				stoppedBy = bounds.ReasonWallClock
				break
			}
			if job.Status == "completed" {
				confirmed++
			}
		}
		return nil
	})
	if err != nil {
		rep.Add("sourcelab/on-demand-verification-completed", false, sanitize(err.Error()))
		return confirmed, stoppedBy, err
	}
	if stoppedBy != "" {
		rep.Add("sourcelab/on-demand-verification-stopped-by-bound", true,
			fmt.Sprintf("confirmed=%d targets=%d stoppedByBound=%q", confirmed, len(targets), stoppedBy))
		rep.Limitations = append(rep.Limitations,
			fmt.Sprintf("按需内容确认因墙钟边界（%s）停止，本次只确认已完成的有界子集", cfg.Limits.MaxWallClock))
	}
	rep.Add("sourcelab/on-demand-verification-completed", stoppedBy != "" || confirmed == len(targets),
		fmt.Sprintf("confirmed=%d targets=%d stoppedByBound=%q", confirmed, len(targets), stoppedBy))
	return confirmed, stoppedBy, nil
}

func unverifiedMediaIDs(sess *environment.Session, libraryID string, limit int) ([]string, error) {
	ctx := context.Background()
	var cursor *string
	ids := []string{}
	for len(ids) < limit {
		listed, err := sess.ListWorks(api.ListWorksParams{LibraryId: &libraryID, Limit: intPtr(50), Cursor: cursor})
		if err == nil && listed.JSON200 == nil && environment.StatusOf(listed) == 404 {
			break
		}
		if err != nil || listed.JSON200 == nil {
			return nil, fmt.Errorf("列出作品失败: status=%d", environment.StatusOf(listed))
		}
		for _, work := range listed.JSON200.Works {
			mediaResp, err := sess.Client.ListWorkMediaWithResponse(ctx, work.Id, &api.ListWorkMediaParams{}, sess.SameOrigin)
			if err != nil || mediaResp.JSON200 == nil {
				return nil, fmt.Errorf("列出作品媒体失败: status=%d", environment.StatusOf(mediaResp))
			}
			for _, item := range mediaResp.JSON200.Media {
				if string(item.ContentVerificationState) != "content_verified" {
					ids = append(ids, item.Id)
					if len(ids) >= limit {
						return ids, nil
					}
				}
			}
		}
		if listed.JSON200.NextCursor == nil || *listed.JSON200.NextCursor == "" {
			break
		}
		cursor = listed.JSON200.NextCursor
	}
	return ids, nil
}

// runIncremental 证明续跑不重做已完成工作。
//
// 判据是产品语义本身：incremental 复用既往摘要时**保留原确认时间**，只有真正重新完成
// 完整哈希才推进它。因此「两次 incremental 之间确认时间折叠哈希不变」等价于「第二次
// 没有重新哈希任何已确认媒体」；同时内容摘要折叠哈希不变说明身份没有漂移。
func runIncremental(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State, previous *State) error {
	first, err := runProfileAndCollect(rep, sess, cfg, guard, state, "incremental", "first")
	if err != nil {
		return err
	}
	second, err := runProfileAndCollect(rep, sess, cfg, guard, state, "incremental", "second")
	if err != nil {
		return err
	}

	rep.Add("sourcelab/incremental-preserves-identity", first.summary.DigestFold == second.summary.DigestFold,
		fmt.Sprintf("firstDigestFold=%s secondDigestFold=%s media=%d/%d",
			shortFold(first.summary.DigestFold), shortFold(second.summary.DigestFold),
			first.summary.MediaCount, second.summary.MediaCount))
	rep.Add("sourcelab/incremental-does-not-rehash", first.summary.VerifiedAtFold == second.summary.VerifiedAtFold,
		fmt.Sprintf("firstVerifiedAtFold=%s secondVerifiedAtFold=%s verified=%d/%d",
			shortFold(first.summary.VerifiedAtFold), shortFold(second.summary.VerifiedAtFold),
			first.summary.VerifiedCount, second.summary.VerifiedCount))
	rep.Add("sourcelab/incremental-second-run-cost", true,
		fmt.Sprintf("firstElapsedMs=%d secondElapsedMs=%d", first.outcome.Elapsed.Milliseconds(), second.outcome.Elapsed.Milliseconds()))

	if previous != nil && previous.DigestFold != "" && previous.VerifiedCount > 0 {
		rep.Add("sourcelab/resumes-previous-run", previous.DigestFold == second.summary.DigestFold ||
			previous.VerifiedAtFold == second.summary.VerifiedAtFold,
			fmt.Sprintf("previousVerified=%d nowVerified=%d previousVerifiedAtFold=%s nowVerifiedAtFold=%s",
				previous.VerifiedCount, second.summary.VerifiedCount,
				shortFold(previous.VerifiedAtFold), shortFold(second.summary.VerifiedAtFold)))
	}

	state.LastProfile = "incremental"
	state.WorkCount, state.MediaCount, state.VerifiedCount = second.summary.WorkCount, second.summary.MediaCount, second.summary.VerifiedCount
	state.DigestFold, state.VerifiedAtFold = second.summary.DigestFold, second.summary.VerifiedAtFold
	return nil
}

// runVerify 是 incremental 复用语义的对照组：强制重新完整哈希必须推进确认时间，而内容
// 身份必须保持不变。没有这个对照，「incremental 没有重做」也可能只是因为它什么都没做。
func runVerify(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard, state *State, previous *State) error {
	before, err := collectMedia(sess, state.LibraryID, bounds.Limits{}, 0)
	if err != nil {
		rep.Add("sourcelab/verify-baseline", false, sanitize(err.Error()))
		return err
	}
	rep.Add("sourcelab/verify-baseline", before.VerifiedCount > 0, before.summary())

	after, err := runProfileAndCollect(rep, sess, cfg, guard, state, "verify", "run")
	if err != nil {
		return err
	}
	rep.Add("sourcelab/verify-preserves-identity", before.DigestFold == after.summary.DigestFold,
		fmt.Sprintf("beforeDigestFold=%s afterDigestFold=%s", shortFold(before.DigestFold), shortFold(after.summary.DigestFold)))
	rep.Add("sourcelab/verify-advances-confirmation-time", before.VerifiedAtFold != after.summary.VerifiedAtFold,
		fmt.Sprintf("beforeVerifiedAtFold=%s afterVerifiedAtFold=%s verified=%d/%d",
			shortFold(before.VerifiedAtFold), shortFold(after.summary.VerifiedAtFold),
			before.VerifiedCount, after.summary.VerifiedCount))

	if previous != nil && previous.DigestFold != "" {
		rep.Add("sourcelab/verify-matches-previous-identity", previous.DigestFold == after.summary.DigestFold,
			fmt.Sprintf("previousDigestFold=%s nowDigestFold=%s", shortFold(previous.DigestFold), shortFold(after.summary.DigestFold)))
	}

	state.LastProfile = "verify"
	state.WorkCount, state.MediaCount, state.VerifiedCount = after.summary.WorkCount, after.summary.MediaCount, after.summary.VerifiedCount
	state.DigestFold, state.VerifiedAtFold = after.summary.DigestFold, after.summary.VerifiedAtFold
	return nil
}

type profileRun struct {
	outcome scanOutcome
	summary mediaSummary
}

func runProfileAndCollect(rep *report.Report, sess *environment.Session, cfg Config, guard *sourceguard.Guard,
	state *State, profile, label string) (profileRun, error) {
	run := profileRun{}
	stage := fmt.Sprintf("scan-%s-%s", profile, label)
	err := guard.Around(stage, func() error {
		var scanErr error
		run.outcome, scanErr = runScan(sess, state.SourceID, state.BindingID, profile, cfg.Limits.MaxWallClock)
		return scanErr
	})
	if err != nil {
		rep.Add("sourcelab/"+stage, false, sanitize(err.Error()))
		return run, err
	}
	ok := run.outcome.Status == "completed" && !run.outcome.Cancelled
	rep.Add("sourcelab/"+stage, ok, run.outcome.summary())
	if !ok {
		return run, fmt.Errorf("%s 扫描未完成: %s", stage, run.outcome.summary())
	}
	summary, err := collectMedia(sess, state.LibraryID, bounds.Limits{}, 0)
	if err != nil {
		rep.Add("sourcelab/"+stage+"-projection", false, sanitize(err.Error()))
		return run, err
	}
	run.summary = summary
	rep.Add("sourcelab/"+stage+"-projection", summary.MediaCount > 0, summary.summary())
	return run, nil
}

func waitForJob(sess *environment.Session, jobID string, timeout time.Duration, cancelAtTimeout bool) (*api.Job, bool, error) {
	deadline := time.Now().Add(timeout)
	cancelRequested := false
	cancelDeadline := time.Time{}
	for {
		resp, err := sess.Client.GetJobWithResponse(context.Background(), jobID, sess.SameOrigin)
		if err != nil {
			return nil, cancelRequested, err
		}
		if resp.JSON200 == nil {
			return nil, cancelRequested, fmt.Errorf("job snapshot 状态 %d", environment.StatusOf(resp))
		}
		status := string(resp.JSON200.Status)
		if status == "completed" || status == "failed" || status == "cancelled" || status == "needs_repair" {
			job := *resp.JSON200
			return &job, cancelRequested, nil
		}
		now := time.Now()
		if !cancelRequested && !now.Before(deadline) {
			if !cancelAtTimeout {
				return nil, false, fmt.Errorf("job 未在 %s 内终止", timeout)
			}
			cancelled, err := sess.Client.CancelJobWithResponse(context.Background(), jobID,
				&api.CancelJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
			if err != nil {
				return nil, true, fmt.Errorf("墙钟边界到达后取消按需确认 Job: %w", err)
			}
			if cancelled.JSON202 == nil {
				return nil, true, fmt.Errorf("墙钟边界到达后取消按需确认 Job: status=%d", environment.StatusOf(cancelled))
			}
			cancelRequested = true
			cancelDeadline = now.Add(30 * time.Second)
		}
		if cancelRequested && !now.Before(cancelDeadline) {
			return nil, true, fmt.Errorf("按需确认 Job 在取消请求后 30s 内未终止")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sanitize 是写进 Finding 之前的最后一道本地防线。report.Add 本身也会脱敏；这里额外
// 折叠 Windows 与 POSIX 形态的路径片段，避免错误文本把真实根带进报告。
func sanitize(text string) string {
	for _, separator := range []string{`\`, `/`} {
		if !strings.Contains(text, separator) {
			continue
		}
		fields := strings.Fields(text)
		for i, field := range fields {
			if strings.Contains(field, separator) && len(field) > 3 {
				fields[i] = "[redacted-path]"
			}
		}
		text = strings.Join(fields, " ")
	}
	return text
}
