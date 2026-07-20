package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/maintenance"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/rules"
)

const IssueProcessInterrupted = "PROCESS_INTERRUPTED"

type Notifier interface {
	JobChanged(jobs.Job)
	PublicationPublished(catalog.Publication)
}

type nopNotifier struct{}

func (nopNotifier) JobChanged(jobs.Job)                      {}
func (nopNotifier) PublicationPublished(catalog.Publication) {}

// Dispatcher 把 Job 交给中央有界调度器执行。未注入时 Service 回退到自管理 goroutine（供不涉及
// 调度器的单元测试使用）。
type Dispatcher interface {
	Submit(jobID string) bool
}

type SpaceGate interface {
	CheckSpace(ctx context.Context, operation string, additionalBytes int64) error
}

type Service struct {
	context     context.Context
	resources   *application.Resources
	jobs        *jobs.Store
	catalog     *catalog.Store
	notifier    Notifier
	wait        sync.WaitGroup
	dispatcher  Dispatcher
	hash        *hashjob.Service
	maintenance *maintenance.Coordinator
	space       SpaceGate
	clock       ports.Clock

	// preReuseHook 仅供确定性测试使用：在本次扫描对某个媒体做最终定位观察（重新 Stat 当前
	// 文件）之前触发一次，覆盖 incremental 复用决策与 index 两条路径，用于模拟 discovery
	// 观察之后、最终定位之前的文件属性变化。生产路径始终为 nil。
	preReuseHook func(relativePath string)
}

// SetPreReuseHook 仅供确定性测试注入；生产 bootstrap 不得调用。
func (s *Service) SetPreReuseHook(hook func(relativePath string)) { s.preReuseHook = hook }

// SetClock 注入内容确认时间来源；未注入时使用系统时钟。测试可注入可推进的时钟以确定性地
// 验证"复用摘要保留旧确认时间、真正完成哈希才推进确认时间"的语义。
func (s *Service) SetClock(value ports.Clock) {
	if value == nil {
		value = clock.System{}
	}
	s.clock = value
}

// SetDispatcher 注入中央调度器；注入后 Start 通过调度器领取执行并接受其 context 取消。
func (s *Service) SetDispatcher(d Dispatcher) { s.dispatcher = d }

// SetHashService 将完整内容哈希交给独立 hash 资源池。未注入时保留同步 fallback，方便
// 仅验证 Catalog 语义的单元测试；正式 bootstrap 始终注入持久 Hash Job Service。
func (s *Service) SetHashService(service *hashjob.Service) { s.hash = service }

func (s *Service) SetMaintenanceCoordinator(coordinator *maintenance.Coordinator) {
	s.maintenance = coordinator
}

func (s *Service) SetSpaceGate(gate SpaceGate) { s.space = gate }

func New(ctx context.Context, resources *application.Resources, jobStore *jobs.Store, catalogStore *catalog.Store, notifier Notifier) (*Service, error) {
	if ctx == nil || resources == nil || jobStore == nil || catalogStore == nil {
		return nil, fmt.Errorf("Scanner 缺少依赖")
	}
	if notifier == nil {
		notifier = nopNotifier{}
	}
	return &Service{context: ctx, resources: resources, jobs: jobStore, catalog: catalogStore, notifier: notifier, clock: clock.System{}}, nil
}

// 扫描档案（scanProfile）决定媒体内容如何被确认，三者互不冒充：
//   - ScanProfileIndex：从不计算完整内容摘要，媒体以 located_unverified 发布，用于首次
//     快速建立可浏览 Catalog；
//   - ScanProfileIncremental：默认档案，按既往观察（同 Source、路径、大小、mtime）组合
//     判断是否可复用既往已确认摘要，只对新增或疑似变化媒体建立 Hash Job；
//   - ScanProfileVerify：忽略既往观察，对本次扫描到的媒体强制重新完整哈希，用于显式、
//     低频的完整性校验。
const (
	ScanProfileIndex       = "index"
	ScanProfileIncremental = "incremental"
	ScanProfileVerify      = "verify"
)

// VerificationTarget 描述本次扫描中必须强制重新完整哈希的单个媒体：discovery 与规则
// 解析仍覆盖整个 Source（保持 SourceWork 拆分/合并结构审查正确），但只有此处列出的
// 媒体跳过 incremental 既往摘要复用短路；未列出的媒体继续按当前 scanProfile 既有规则
// 处理（复用、跳过或按需新增哈希），不因存在 target 而改变其余媒体的正确性。
type VerificationTarget struct {
	MediaID                string `json:"mediaId"`
	SourceID               string `json:"sourceId"`
	RelativePath           string `json:"relativePath"`
	ObservationFingerprint string `json:"observationFingerprint,omitempty"`
}

type scanRequest struct {
	ScanProfile         string               `json:"scanProfile,omitempty"`
	VerificationTargets []VerificationTarget `json:"verificationTargets,omitempty"`
}

// validateScanProfile 只接受空字符串（表示未显式指定，交由调用方按 Source 是否已发布决定
// 实际档案）与三个正式档案值；任何拼写错误或未知值都必须返回结构化 VALIDATION_ERROR，
// 不得静默归一化为 incremental。
func validateScanProfile(value string) (string, error) {
	switch value {
	case "", ScanProfileIndex, ScanProfileIncremental, ScanProfileVerify:
		return value, nil
	default:
		return "", fault.WithField(fault.CodeValidation, "scanProfile", nil)
	}
}

func scanProfileFromJob(job jobs.Job) string {
	if len(job.RequestJSON) == 0 {
		return ScanProfileIncremental
	}
	var request scanRequest
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil {
		return ScanProfileIncremental
	}
	if request.ScanProfile == ScanProfileIndex || request.ScanProfile == ScanProfileVerify {
		return request.ScanProfile
	}
	return ScanProfileIncremental
}

// verificationTargetsFromJob 还原本次 Job 冻结的目标媒体集合，按规范化相对路径索引，
// 保留完整冻结字段（MediaID、ObservationFingerprint）供执行阶段真正验证——不能只用
// RelativePath 存在与否判断"是否是目标"，冻结的 Media 身份与 observation 指纹必须参与
// 执行阶段的一致性检查，否则请求时冻结的语义在执行时会被静默丢弃。同一 Job 的重试
// Attempt 复用同一 RequestJSON，因此目标集合天然与首次入队时一致。
func verificationTargetsFromJob(job jobs.Job) map[string]VerificationTarget {
	if len(job.RequestJSON) == 0 {
		return nil
	}
	var request scanRequest
	if err := json.Unmarshal(job.RequestJSON, &request); err != nil || len(request.VerificationTargets) == 0 {
		return nil
	}
	result := make(map[string]VerificationTarget, len(request.VerificationTargets))
	for _, target := range request.VerificationTargets {
		result[target.RelativePath] = target
	}
	return result
}

func (s *Service) CreateScan(ctx context.Context, sourceID, createdBy string) (jobs.Job, error) {
	return s.CreateScanWithIdempotency(ctx, sourceID, createdBy, "")
}

func (s *Service) CreateScanWithIdempotency(ctx context.Context, sourceID, createdBy, idempotencyKey string) (jobs.Job, error) {
	return s.CreateScanWithProfile(ctx, sourceID, createdBy, idempotencyKey, "")
}

// CreateScanWithProfile 是唯一实际创建扫描 Job 的入口。scanProfile 为空表示未显式指定，
// 按下表选择最终档案，两种情况下持久化的都是最终实际决定的档案，绝不保存含糊的 "auto"：
//
//	无当前 publication，且 control.db 无持久领域历史 -> index
//	有当前 publication                            -> incremental
//	无当前 publication，但 control.db 有持久领域历史 -> incremental
//
// 持久领域历史见 Resources.SourceHasDurableHistory：Catalog 可随时删除重建，但 control.db
// 中残留的 Binding/Binding issue/结构决策说明该 Source 曾经完成过真正的 Canonical 解析，
// 仅凭"当前无 publication"判断为全新 Source 会让本次扫描不建立 ContentBlob digest，绕过
// 阶段 1 依赖完整哈希证据的 SourceWork 拆分/合并结构审查。显式指定的 index/incremental/
// verify 必须被尊重；已有 publication 或 Catalog 已丢失但仍有持久领域历史时显式请求 index
// 都会绕过该审查，因此拒绝并保持 Binding/Catalog 不变，不创建 Job。
func (s *Service) CreateScanWithProfile(ctx context.Context, sourceID, createdBy, idempotencyKey, scanProfile string) (jobs.Job, error) {
	return s.createScan(ctx, sourceID, createdBy, idempotencyKey, scanProfile, nil)
}

// CreateVerificationScan 建立一个只强制 targets 中媒体重新完整哈希的 incremental 扫描
// Job，取代"单媒体确认=整 Source verify"的旧语义。targets 必须全部属于 sourceID，且
// 该 Source 必须已有 publication（单媒体按需确认只对已发布 Catalog 中的已知媒体有意义，
// 因此始终显式使用 incremental，不落入 index/首次扫描判定）。Source discovery 和规则
// 解析仍完整执行，其余媒体按既有 incremental 规则正常处理，不因存在 target 而改变。
func (s *Service) CreateVerificationScan(ctx context.Context, sourceID, createdBy, idempotencyKey string, targets []VerificationTarget) (jobs.Job, error) {
	if len(targets) == 0 {
		return jobs.Job{}, fault.WithField(fault.CodeValidation, "verificationTargets", nil)
	}
	seen := make(map[string]struct{}, len(targets))
	normalizedTargets := make([]VerificationTarget, len(targets))
	for index, target := range targets {
		if target.SourceID != sourceID {
			return jobs.Job{}, fault.WithField(fault.CodeValidation, "verificationTargets", nil)
		}
		if _, err := domain.ParseID(domain.IDCanonicalMedia, target.MediaID); err != nil {
			return jobs.Job{}, fault.WithField(fault.CodeValidation, "verificationTargets", err)
		}
		normalized, err := media.ValidateRelativePath(target.RelativePath)
		if err != nil {
			return jobs.Job{}, fault.WithField(fault.CodeValidation, "verificationTargets", err)
		}
		if _, dup := seen[normalized]; dup {
			return jobs.Job{}, fault.WithField(fault.CodeValidation, "verificationTargets", nil)
		}
		seen[normalized] = struct{}{}
		target.RelativePath = normalized
		normalizedTargets[index] = target
	}
	published, err := s.catalog.SourcePublished(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, err
	}
	if !published {
		return jobs.Job{}, fault.New(fault.CodeConflict, false, nil)
	}
	return s.createScan(ctx, sourceID, createdBy, idempotencyKey, ScanProfileIncremental, normalizedTargets)
}

func (s *Service) createScan(ctx context.Context, sourceID, createdBy, idempotencyKey, scanProfile string, targets []VerificationTarget) (jobs.Job, error) {
	requested, err := validateScanProfile(scanProfile)
	if err != nil {
		return jobs.Job{}, err
	}
	if _, err := s.resources.GetSource(ctx, sourceID); err != nil {
		return jobs.Job{}, err
	}
	published, err := s.catalog.SourcePublished(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, err
	}
	durableHistory, err := s.resources.SourceHasDurableHistory(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, err
	}
	effective := requested
	switch requested {
	case "":
		if published || durableHistory {
			effective = ScanProfileIncremental
		} else {
			effective = ScanProfileIndex
		}
	case ScanProfileIndex:
		if published || durableHistory {
			return jobs.Job{}, fault.New(fault.CodeConflict, false, nil)
		}
	}
	binding, err := s.resources.BindingForSource(ctx, sourceID)
	if err != nil {
		return jobs.Job{}, err
	}
	version, err := s.resources.GetRuleVersion(ctx, binding.SemanticHash)
	if err != nil {
		return jobs.Job{}, err
	}
	snapshot := &jobs.RuleExecutionSnapshot{
		SemanticHash: binding.SemanticHash, Parameters: append([]byte(nil), binding.Parameters...),
		ParametersHash: application.RuleParameterHash(binding.Parameters), RuleIRHash: binding.RuleIRHash,
		CompilerVersion: rules.CompilerVersion, CELProfileVersion: rules.CELProfileVersion,
		ExtensionRegistryVersion: version.IR.ExtensionRegistryVersion,
	}
	requestJSON, err := json.Marshal(scanRequest{ScanProfile: effective, VerificationTargets: targets})
	if err != nil {
		return jobs.Job{}, fault.New(fault.CodeInternal, true, err)
	}
	job, err := s.jobs.CreateScanWithOptions(ctx, sourceID, createdBy, "", snapshot, jobs.CreateOptions{
		ResourceClass: jobs.ResourceScan, IdempotencyKey: idempotencyKey, RequestJSON: requestJSON,
	})
	if err == nil {
		s.notifier.JobChanged(job)
	}
	return job, err
}

func (s *Service) Start(jobID string) {
	if s.dispatcher != nil {
		s.dispatcher.Submit(jobID)
		return
	}
	s.wait.Add(1)
	go func() { defer s.wait.Done(); _ = s.Execute(s.context, jobID) }()
}

func (s *Service) Wait() { s.wait.Wait() }

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.Start(ctx, jobID)
	if err != nil {
		return err
	}
	// 同一逻辑 Job 的新 Attempt 复用 Job ID；先清理上次未发布候选，避免把中断的 staging
	// 当成本次输入。活动 publication 不受影响。
	_ = s.catalog.AbortCandidate(ctx, jobID)
	s.notifier.JobChanged(job)
	if s.space != nil {
		if err := s.space.CheckSpace(ctx, "catalog_staging", 0); err != nil {
			return s.fail(ctx, job.ID, err)
		}
	}
	source, err := s.resources.GetSource(ctx, job.SourceID)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	var binding application.SourceRuleBinding
	if job.RuleSemanticHash != "" {
		version, snapshotErr := s.resources.GetRuleVersion(ctx, job.RuleSemanticHash)
		if snapshotErr != nil {
			return s.fail(ctx, job.ID, snapshotErr)
		}
		compiled, compileErr := rules.CompilePackage(version.Canonical)
		if compileErr != nil {
			return s.fail(ctx, job.ID, compileErr)
		}
		ir, irHash, parameters, compileErr := rules.CompileBinding(compiled, job.RuleParameters)
		if compileErr != nil || irHash != job.RuleIRHash {
			if compileErr == nil {
				compileErr = fmt.Errorf("Job 规则快照 RuleIRHash 不匹配")
			}
			return s.fail(ctx, job.ID, compileErr)
		}
		binding = application.SourceRuleBinding{SourceID: source.ID, SemanticHash: job.RuleSemanticHash, Parameters: parameters, RuleIRHash: irHash, IR: ir}
	} else {
		binding, err = s.resources.BindingForSource(ctx, source.ID)
		if err != nil {
			return s.fail(ctx, job.ID, err)
		}
	}
	discovered, err := discover(ctx, source.RootPath, binding.IR, binding.Parameters)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	if len(discovered) == 0 {
		return s.fail(ctx, job.ID, fault.New(fault.CodeRuleEval, false, nil))
	}
	total := int64(0)
	for _, work := range discovered {
		total += int64(len(work.Media))
	}
	if total == 0 {
		return s.fail(ctx, job.ID, fault.New(fault.CodeRuleEval, false, nil))
	}
	scanProfile := scanProfileFromJob(job)
	forcedTargets := verificationTargetsFromJob(job)
	visitedTargets := make(map[string]struct{}, len(forcedTargets))
	current := int64(0)
	verificationState := make(map[string]string, total)  // relative path -> state
	lastConfirmedAt := make(map[string]time.Time, total) // relative path -> confirmation time
	for workIndex := range discovered {
		for mediaIndex := range discovered[workIndex].Media {
			select {
			case <-ctx.Done():
				return s.fail(ctx, job.ID, fault.New(fault.CodeProcessInterrupted, true, ctx.Err()))
			default:
			}
			item := &discovered[workIndex].Media[mediaIndex]
			skipHash := false
			target, forced := forcedTargets[item.RelativePath]
			if forced {
				visitedTargets[item.RelativePath] = struct{}{}
				if err := s.verifyObservationUnchanged(ctx, source.ID, target, item.ExpectedSize, item.ExpectedModTimeNanos); err != nil {
					return s.fail(ctx, job.ID, err)
				}
			}
			if scanProfile != ScanProfileVerify && !forced {
				prior, lookupErr := s.catalog.LookupPriorObservation(ctx, source.ID, item.RelativePath)
				if lookupErr != nil {
					return s.fail(ctx, job.ID, lookupErr)
				}
				if prior.Found && prior.ContentVerificationState == catalog.ContentVerificationStateContentVerified &&
					prior.Size == item.ExpectedSize && prior.MTimeNanos == item.ExpectedModTimeNanos {
					// 组合身份证据（同 Source、规范化路径、大小、mtime）未变化，且既往已完成完整
					// 确认：候选复用既往摘要。但 discovery 阶段的 Stat 与此刻之间存在窗口，
					// 复用前必须重新打开并 Stat 当前文件；只有这次重新句柄得到的 size/mtime
					// 也同时与 discovery 观察一致才允许复用，任一证据在此窗口内变化都必须放弃
					// 复用、降级为下方完整 Hash Job，不得把旧 digest 与新文件属性混合发布。
					if s.preReuseHook != nil {
						s.preReuseHook(item.RelativePath)
					}
					located, locateErr := media.LocateSourceFile(source.RootPath, item.RelativePath)
					if locateErr != nil {
						return s.fail(ctx, job.ID, locateErr)
					}
					if located.Size == item.ExpectedSize && located.ModTimeNanos == item.ExpectedModTimeNanos {
						blob, blobErr := domain.ParseContentBlobRef(prior.Algorithm, prior.Digest)
						if blobErr != nil {
							return s.fail(ctx, job.ID, blobErr)
						}
						item.Hash = media.HashResult{
							Blob: blob, Size: located.Size, LocationKey: located.LocationKey, RelativePath: located.RelativePath,
						}
						verificationState[item.RelativePath] = catalog.ContentVerificationStateContentVerified
						// 复用摘要不改变确认时间：保留既往真正完成完整哈希时的时间，不得因为
						// 本次只是复用旧摘要就把 last_confirmed_at 推进到现在。
						lastConfirmedAt[item.RelativePath] = prior.LastConfirmedAt
						skipHash = true
					} else {
						// 复用前证据已变化：放弃复用，改走下方完整 Hash Job；以刚获得的当前
						// 状态作为本次哈希的期望身份，避免与已经过期的 discovery 观察比较、
						// 对本来只是稍后处理的合法文件产生误报的 CONTENT_CHANGED_DURING_HASH。
						item.ExpectedSize = located.Size
						item.ExpectedModTimeNanos = located.ModTimeNanos
					}
				} else if scanProfile == ScanProfileIndex ||
					(len(forcedTargets) > 0 && prior.Found && prior.ContentVerificationState == catalog.ContentVerificationStateLocatedUnverified) {
					// index 档案对新增或疑似变化媒体也不建立 Hash Job，只定位并标记未确认；
					// 目标化确认扫描（forcedTargets 非空）额外把这一规则用于非目标的既有
					// located_unverified 媒体——它们不是本次请求要强制确认的目标，即使
					// scanProfile 是 incremental 也不得因为"存在 target"就被顺带强制哈希，
					// 必须继续保持未确认、不产生 Hash Job。一条 FileObservation 的 size、
					// mtime、location key 必须来自同一次最终定位观察：discovery 阶段的 Stat
					// 与这次 Locate 之间可能存在窗口，只更新 Size 而保留 discovery 时的旧
					// ExpectedModTimeNanos 会持久化"当前 size + 较早 mtime"的混合记录，因此
					// 这里必须同步刷新 ExpectedModTimeNanos。
					if s.preReuseHook != nil {
						s.preReuseHook(item.RelativePath)
					}
					located, locateErr := media.LocateSourceFile(source.RootPath, item.RelativePath)
					if locateErr != nil {
						return s.fail(ctx, job.ID, locateErr)
					}
					item.Hash = media.HashResult{Size: located.Size, LocationKey: located.LocationKey, RelativePath: located.RelativePath}
					item.ExpectedModTimeNanos = located.ModTimeNanos
					verificationState[item.RelativePath] = catalog.ContentVerificationStateLocatedUnverified
					skipHash = true
				}
			}
			if !skipHash {
				var hashed media.HashResult
				var hashErr error
				if s.hash != nil {
					hashJob, createErr := s.hash.Create(ctx, hashjob.Request{SourceID: source.ID, RelativePath: item.RelativePath,
						ExpectedSize: item.ExpectedSize, ExpectedModTimeNanos: item.ExpectedModTimeNanos,
						HasExpectedIdentity: item.HasExpectedIdentity, ParentJobID: job.ID}, job.CreatedBy)
					if createErr == nil && (hashJob.Status == jobs.StatusFailed || hashJob.Status == jobs.StatusCancelled || hashJob.Status == jobs.StatusNeedsRepair) {
						hashJob, createErr = s.jobs.Retry(ctx, hashJob.ID, job.CreatedBy)
					}
					if createErr == nil && hashJob.Status != jobs.StatusCompleted {
						s.hash.Start(hashJob.ID)
					}
					if createErr == nil {
						hashed, hashErr = s.hash.WaitResult(ctx, hashJob.ID)
					} else {
						hashErr = createErr
					}
				} else {
					hashed, hashErr = media.HashSourceFile(source.RootPath, item.RelativePath, nil)
				}
				if hashErr != nil {
					return s.fail(ctx, job.ID, hashErr)
				}
				item.Hash = hashed
				verificationState[item.RelativePath] = catalog.ContentVerificationStateContentVerified
				// 只有真正完成完整哈希才推进确认时间。
				lastConfirmedAt[item.RelativePath] = s.clock.Now().UTC()
			}
			current++
			job, err = s.jobs.Progress(ctx, job.ID, "hashing", current, total)
			if err != nil {
				return err
			}
			s.notifier.JobChanged(job)
		}
	}
	// 目标消失检查：discovery 只遍历磁盘上真实存在的媒体，一个冻结目标如果对应的文件已经
	// 被移动、改名或删除，它的 RelativePath 根本不会出现在 discovered 中，上面的循环也就
	// 不会把它标记为已访问。多目标时必须逐个确认全部命中，不能因为其它目标成功就静默
	// 当作整体成功发布一个没有完成用户请求的 Catalog。
	for relativePath := range forcedTargets {
		if _, visited := visitedTargets[relativePath]; !visited {
			return s.fail(ctx, job.ID, fault.New(fault.CodeContentDisappeared, false, nil))
		}
	}
	canonicalInput := make([]application.DiscoveredWork, 0, len(discovered))
	for _, work := range discovered {
		mediaItems := make([]application.DiscoveredMedia, len(work.Media))
		for index := range work.Media {
			mediaItems[index] = application.DiscoveredMedia{SourceKey: work.Media[index].SourceKey,
				RuleKey: work.Media[index].RuleKey, Algorithm: work.Media[index].Hash.Blob.Algorithm,
				Digest: work.Media[index].Hash.Blob.Digest, Ordinal: index}
		}
		canonicalInput = append(canonicalInput, application.DiscoveredWork{SourceKey: work.SourceKey,
			ProviderID: work.ProviderID, ExternalID: work.ExternalID, Title: work.Title,
			Creator: creatorReference(work), Media: mediaItems})
	}
	canonical, err := s.resources.EnsureCanonical(ctx, source.ID, canonicalInput)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	// 目标身份一致性检查：请求时冻结的 MediaID 必须恰好解析到本次扫描实际确认的媒体。
	// discovery/规则/Binding 结构决策可能让同一相对路径在拆分、合并或重新绑定后对应到
	// 不同的 CanonicalMedia；仅凭路径存在无法证明它仍是请求方原本指定的那个媒体，必须
	// 显式比对，不一致时返回结构化 VERIFICATION_TARGET_MISMATCH，不得静默确认错误对象。
	for _, work := range discovered {
		canonicalWork := canonical[work.SourceKey]
		for _, item := range work.Media {
			target, forced := forcedTargets[item.RelativePath]
			if !forced {
				continue
			}
			if canonicalWork.Media[item.SourceKey].ID != target.MediaID {
				return s.fail(ctx, job.ID, fault.New(fault.CodeVerificationTargetMismatch, false, nil))
			}
		}
	}
	overlays, creatorMerges, controlWatermark, err := s.resources.QueryOverlaySnapshot(ctx, nil)
	if err != nil {
		return s.fail(ctx, job.ID, err)
	}
	candidate, err := s.catalog.BeginCandidate(ctx, job.ID, source.ID, controlWatermark)
	if err != nil {
		if isCatalogCandidatePublished(err) {
			return s.recoverAlreadyPublished(ctx, job.ID)
		}
		return s.fail(ctx, job.ID, err)
	}
	works := make([]catalog.WorkFact, 0, len(discovered))
	mediaFacts := make([]catalog.MediaFact, 0, total)
	for _, work := range discovered {
		canonicalWork := canonical[work.SourceKey]
		filenames := make([]string, 0, len(work.Media))
		for _, item := range work.Media {
			filenames = append(filenames, path.Base(item.RelativePath))
		}
		workFact := catalog.WorkFact{SourceID: source.ID, LibraryID: source.LibraryID,
			SourceKey: work.SourceKey, ProviderID: work.ProviderID, ExternalID: work.ExternalID,
			SourceTitle: canonicalWork.Title, SourceTags: work.Tags,
			Title: canonicalWork.Title, Creator: work.Creator, Tags: work.Tags,
			Filenames: filenames, WorkID: canonicalWork.ID}
		if len(canonicalWork.Creators) > 0 {
			creator := creatorReference(work)
			workFact.Creator = canonicalWork.Creators[0].Name
			workFact.CreatorID = canonicalWork.Creators[0].ID
			workFact.CreatorSourceKey = creator.SourceKey
			workFact.CreatorProviderID = creator.ProviderID
			workFact.CreatorExternalID = creator.ExternalID
			workFact.SourceCreatorName = work.Creator
		}
		works = append(works, workFact)
		for _, item := range work.Media {
			canonicalMedia := canonicalWork.Media[item.SourceKey]
			state := verificationState[item.RelativePath]
			if state == "" {
				state = catalog.ContentVerificationStateContentVerified
			}
			fact := catalog.MediaFact{
				SourceID: source.ID, SourceKey: item.SourceKey, WorkSourceKey: work.SourceKey, RuleKey: item.RuleKey,
				RelativePath: item.Hash.RelativePath, Kind: item.Kind, MIME: item.MIME, Size: item.Hash.Size,
				Algorithm: item.Hash.Blob.Algorithm, Digest: item.Hash.Blob.Digest, LocationKey: item.Hash.LocationKey,
				MediaID: canonicalMedia.ID, WorkID: canonicalWork.ID, Ordinal: canonicalMedia.Ordinal,
				ContentVerificationState: state, MTimeNanos: item.ExpectedModTimeNanos,
			}
			if state == catalog.ContentVerificationStateContentVerified {
				fact.LastConfirmedAlgorithm = item.Hash.Blob.Algorithm
				fact.LastConfirmedDigest = item.Hash.Blob.Digest
				if confirmedAt, ok := lastConfirmedAt[item.RelativePath]; ok {
					fact.LastConfirmedAt = confirmedAt
				} else {
					fact.LastConfirmedAt = time.Now().UTC()
				}
			}
			mediaFacts = append(mediaFacts, fact)
		}
	}
	if err := s.catalog.Stage(ctx, candidate, works, mediaFacts); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	overlayFacts := make(map[string]catalog.OverlayFact, len(overlays))
	for workID, value := range overlays {
		overlayFacts[workID] = catalog.OverlayFact{TitleOverride: value.TitleOverride, ManualTags: value.ManualTags,
			Hidden: value.Hidden, CustomCoverMediaID: value.CustomCoverMediaID,
			Favorite: value.Favorite, Progress: value.Progress}
	}
	if err := s.catalog.ApplyCatalogCandidateOverlays(ctx, candidate, overlayFacts); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	if err := s.catalog.ApplyCreatorMerges(ctx, candidate.CatalogRevisionID, candidate.OverlayRevisionID, creatorMerges); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	if err := s.catalog.ValidateCandidate(ctx, candidate); err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	job, err = s.jobs.BeginPublishing(ctx, job.ID)
	if err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return err
	}
	s.notifier.JobChanged(job)
	releasePublication := s.maintenance.AcquirePublication()
	publication, err := s.catalog.Publish(ctx, candidate)
	releasePublication()
	if err != nil {
		_ = s.catalog.AbortCandidate(ctx, job.ID)
		return s.fail(ctx, job.ID, err)
	}
	if err := s.resources.MarkOverlaySnapshotPublished(ctx, publication.ControlWatermark, publication.ID); err != nil {
		return err
	}
	s.notifier.PublicationPublished(publication)
	job, err = s.jobs.Complete(ctx, job.ID, publication.ID)
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	nonterminal, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing)
	if err != nil {
		return err
	}
	for _, job := range nonterminal {
		if job.Type != "scan" {
			continue
		}
		if job.Status == jobs.StatusQueued {
			continue
		}
		publication, publicationErr := s.catalog.PublicationForJob(ctx, job.ID)
		if publicationErr == nil && (job.Status == jobs.StatusRunning || job.Status == jobs.StatusPublishing) {
			if markErr := s.resources.MarkOverlaySnapshotPublished(ctx, publication.ControlWatermark, publication.ID); markErr != nil {
				return markErr
			}
			recovered, recoverErr := s.jobs.RecoverCompleted(ctx, job.ID, publication.ID)
			if recoverErr != nil {
				return recoverErr
			}
			s.notifier.JobChanged(recovered)
			continue
		}
		if publicationErr != nil && !isNotFound(publicationErr) {
			return publicationErr
		}
		// 未发布的 running/publishing Job 由中央租约回收循环在 lease 真正过期后收敛；
		// 启动时 lease 尚有效不能提前判死。
	}
	completed, err := s.jobs.ListByStatuses(ctx, jobs.StatusCompleted)
	if err != nil {
		return err
	}
	for _, job := range completed {
		if job.Type != "scan" {
			continue
		}
		if _, publicationErr := s.catalog.PublicationForJob(ctx, job.ID); isNotFound(publicationErr) {
			repaired, repairErr := s.jobs.MarkNeedsRepair(ctx, job.ID, string(fault.CodeCatalogPublicationAbsent))
			if repairErr != nil {
				return repairErr
			}
			s.notifier.JobChanged(repaired)
		} else if publicationErr != nil {
			return publicationErr
		}
	}
	terminal, err := s.jobs.ListByStatuses(ctx, jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair)
	if err != nil {
		return err
	}
	for _, job := range terminal {
		if job.Type == "scan" {
			_ = s.catalog.AbortCandidate(ctx, job.ID)
		}
	}
	return nil
}

func (s *Service) fail(ctx context.Context, jobID string, cause error) error {
	current, _ := s.jobs.Get(context.Background(), jobID)
	if current.CancelRequested || errors.Is(ctx.Err(), context.Canceled) {
		if cancelled, cancelErr := s.jobs.FinalizeCancelled(context.Background(), jobID); cancelErr == nil {
			s.notifier.JobChanged(cancelled)
			return cause
		}
	}
	code := faultCode(cause)
	retryable := true
	var structured *fault.Error
	if errors.As(cause, &structured) {
		retryable = structured.Retryable
	}
	failed, err := s.jobs.FailWithRetryable(ctx, jobID, string(code), retryable)
	if err == nil {
		s.notifier.JobChanged(failed)
	}
	return cause
}

func faultCode(err error) fault.Code {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return structured.Code
	}
	return fault.CodeInternal
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeNotFound
}

func isCatalogCandidatePublished(err error) bool {
	if err == nil {
		return false
	}
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeCatalogCandidatePublished
}

// verifyObservationUnchanged 校验一个冻结 VerificationTarget 的 ObservationFingerprint
// 与执行时刻的真实观察是否一致。currentSize/currentModTimeNanos 必须来自本次执行开头
// discover() 对磁盘的新鲜 Stat（而不是再次读取磁盘或复用 Catalog 里可能早已过期的旧
// 记录），据此判断请求排队期间文件是否已经被替换、截断或以不同内容重新写入；同时把
// Catalog 当前记录的 content_verification_state 与冻结指纹比较，判断是否已被另一个
// 并发确认抢先完成。空 ObservationFingerprint 表示调用方未提供冻结指纹，跳过该项
// 校验，仍然依赖调用方在循环中执行的目标消失与 MediaID 一致性检查。
func (s *Service) verifyObservationUnchanged(ctx context.Context, sourceID string, target VerificationTarget, currentSize, currentModTimeNanos int64) error {
	if target.ObservationFingerprint == "" {
		return nil
	}
	prior, err := s.catalog.LookupPriorObservation(ctx, sourceID, target.RelativePath)
	if err != nil {
		return err
	}
	current := fmt.Sprintf("%d:%d:%s", currentSize, currentModTimeNanos, prior.ContentVerificationState)
	if current != target.ObservationFingerprint {
		return fault.New(fault.CodeContentChangedDuringHash, true, nil)
	}
	return nil
}

// recoverAlreadyPublished 处理 BeginCandidate 检测到的 Saga gap：该 Job 已经真正完成过
// Catalog 发布，只是 control 侧尚未收到 completed。不得再次构建或再次发布，只把已有
// publication 对账为 control 侧 completed，且不重复发出 PublicationPublished 事件——那是
// 首次发布时已经交付过的依赖通知。
func (s *Service) recoverAlreadyPublished(ctx context.Context, jobID string) error {
	publication, err := s.catalog.PublicationForJob(ctx, jobID)
	if err != nil {
		return s.fail(ctx, jobID, err)
	}
	if err := s.resources.MarkOverlaySnapshotPublished(ctx, publication.ControlWatermark, publication.ID); err != nil {
		return err
	}
	job, err := s.jobs.RecoverCompleted(ctx, jobID, publication.ID)
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
}

type discoveredWork struct {
	SourceKey, ProviderID, ExternalID, Title, Creator string
	Tags                                              []string
	Media                                             []discoveredMedia
}
type discoveredMedia struct {
	SourceKey, RuleKey, RelativePath, Kind, MIME string
	ExpectedSize                                 int64
	ExpectedModTimeNanos                         int64
	HasExpectedIdentity                          bool
	Hash                                         media.HashResult
}

func creatorReference(work discoveredWork) application.DiscoveredCreator {
	if work.Creator == "" {
		return application.DiscoveredCreator{}
	}
	workReference := work.SourceKey
	if work.ExternalID != "" {
		workReference = "origin:" + work.ProviderID + ":" + work.ExternalID
	}
	return application.DiscoveredCreator{
		SourceKey:  workReference + "/creator:primary:0",
		ProviderID: work.ProviderID,
		Name:       work.Creator,
	}
}

func discover(ctx context.Context, root string, ir rules.RuleIR, parameters []byte) ([]discoveredWork, error) {
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		return nil, fault.New(fault.CodeRuleEval, false, err)
	}
	var result []discoveredWork
	err = filepath.WalkDir(root, func(onDisk string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() || onDisk == root {
			return nil
		}
		relativeOS, err := filepath.Rel(root, onDisk)
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(relativeOS)
		matched, err := path.Match(ir.WorkDirectoryGlob, relative)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		children, err := os.ReadDir(onDisk)
		if err != nil {
			return err
		}
		sample := rules.DryRunInput{Path: relative, Metadata: map[string]any{}, Files: []rules.DryRunFile{}}
		if ir.MetadataFile != "" {
			metadataPath := filepath.Join(onDisk, filepath.FromSlash(ir.MetadataFile))
			info, statErr := os.Stat(metadataPath)
			if statErr != nil || info.Size() > int64(rules.CELProfileV1.InputJSONBytes) {
				return fmt.Errorf("metadata 不可用或超限")
			}
			content, readErr := os.ReadFile(metadataPath)
			if readErr != nil {
				return readErr
			}
			if err := json.Unmarshal(content, &sample.Metadata); err != nil {
				return fmt.Errorf("metadata 损坏: %w", err)
			}
		}
		for _, child := range children {
			if child.IsDir() || child.Type()&fs.ModeSymlink != 0 || child.Name() == ir.MetadataFile {
				continue
			}
			info, err := child.Info()
			if err != nil {
				return err
			}
			sample.Files = append(sample.Files, rules.DryRunFile{Path: child.Name(), Size: info.Size()})
		}
		evaluated, err := lifecycle.EvaluateIR(ctx, ir, parameters, sample)
		if err != nil {
			return err
		}
		if evaluated.Work.Ignored {
			return filepath.SkipDir
		}
		work := discoveredWork{SourceKey: evaluated.Work.StableKey, ProviderID: evaluated.Work.ProviderID, ExternalID: evaluated.Work.ExternalID,
			Title: evaluated.Work.Title, Creator: evaluated.Work.Creator, Tags: evaluated.Work.Tags}
		for _, item := range evaluated.Work.Media {
			mediaRelative := path.Join(relative, item.Path)
			if _, err := media.ValidateRelativePath(mediaRelative); err != nil {
				return err
			}
			mediaPath := filepath.Join(root, filepath.FromSlash(mediaRelative))
			info, infoErr := os.Stat(mediaPath)
			if infoErr != nil {
				return infoErr
			}
			work.Media = append(work.Media, discoveredMedia{SourceKey: path.Join(work.SourceKey, item.StableKey),
				RuleKey: item.StableKey, RelativePath: mediaRelative, Kind: item.Kind, MIME: item.MIME})
			work.Media[len(work.Media)-1].ExpectedSize = info.Size()
			work.Media[len(work.Media)-1].ExpectedModTimeNanos = info.ModTime().UnixNano()
			work.Media[len(work.Media)-1].HasExpectedIdentity = true
		}
		if len(work.Media) > 0 {
			result = append(result, work)
		}
		return filepath.SkipDir
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fault.New(fault.CodeSourceUnavailable, true, err)
		}
		return nil, fault.New(fault.CodeRuleEval, false, err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceKey < result[j].SourceKey })
	return result, nil
}

func pathOnDisk(root, relative string) string {
	return root + string(os.PathSeparator) + path.Clean(relative)
}
