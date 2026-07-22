// Package corpus 定义阶段 4 正式压力测试使用的确定性合成语料生成规则。
//
// 本包不导入任何 internal/* 包：seed 工具（导入 internal/catalog 等生产内部包，
// 直接写入 catalog.db）与 probe 工具（只导入 pkg/galleryapi 与标准库，经由真实
// galleryd 的公开 HTTP API 驱动）共同依赖这份纯函数定义。"确定性"只覆盖内容、
// 数量、排序与关系（标题、标签、隐藏/收藏比例、媒体种类分布等），不覆盖 Library/
// Source/Work/Media/Creator 的公开领域 ID——那些 ID 必须由 seed 工具通过与生产
// 完全一致的 internal/platform/identity.Generator 生成（真实 UUIDv7 + 类型前缀），
// 不能是本包里可预测的字符串；早期草稿曾用 "wrk_stage4_00000000" 之类的手写 ID，
// 与 cursor.schema.json 对 lastCanonicalWorkId 的 UUIDv7 pattern 校验不兼容，导致
// 命中数超过一页时签发游标必定返回 CURSOR_INVALID——这是测试夹具缺陷，不是产品
// 缺陷，修复方式是让 seed 只用正式 ID 生成器，不是放宽生产端的 schema 校验。
package corpus

import (
	"encoding/json"
	"fmt"
	"os"
)

// SpecialCJKMarker 是选择性中文搜索标记：出现在每 1000 个作品中固定余数为
// specialCJKOffset 的那一个。
const SpecialCJKMarker = "关键词命中"

// SpecialLatinMarker 是拉丁大小写折叠标记：出现在每 1000 个作品中固定余数为
// specialLatinOffset 的那一个。
const SpecialLatinMarker = "Keyword"

// UniqueFilenameMarker 是文件名中缀标记的前缀：出现在每 500 个作品中固定余数为
// uniqueFilenameOffset 的那一个。
const UniqueFilenameMarker = "uniquefile-marker"

const (
	// CreatorCount 是合成语料使用的创作者槽位总数。
	CreatorCount  = 24
	providerCount = 5
	tagPoolSize   = 40

	// 特殊标记使用的余数偏移量刻意选择为非零、且与 Hidden（i%50）、Favorite（i%20）
	// 的周期互质/不整除，避免出现"标记恰好总是落在隐藏作品上"这种确定性但意外的
	// 混淆——1000 和 500 都是 50 的倍数，若继续用余数 0 作为标记位置，该位置在
	// i%50 下恒为 0（即恒为 Hidden），会让搜索命中数系统性地被默认隐藏过滤器清零，
	// 这是曾经真实发生过的测试夹具缺陷（HARNESS_BUG），不是产品缺陷。
	specialCJKOffset     = 3
	specialLatinOffset   = 1
	uniqueFilenameOffset = 7
)

// Title 返回第 i 个作品（0 基）的标题。
func Title(i int) string {
	switch {
	case i%1000 == specialCJKOffset:
		return fmt.Sprintf("阶段四特别作品 %s %06d", SpecialCJKMarker, i)
	case i%1000 == specialLatinOffset:
		return fmt.Sprintf("Stage4 SPECIAL %s %06d", SpecialLatinMarker, i)
	default:
		return fmt.Sprintf("普通作品 %06d", i)
	}
}

// Filename 返回第 i 个作品唯一媒体的文件名（仅 basename）。
func Filename(i int) string {
	if i%500 == uniqueFilenameOffset {
		return fmt.Sprintf("%s-%07d.png", UniqueFilenameMarker, i)
	}
	return fmt.Sprintf("gallery-middle-%07d.jpg", i)
}

// CreatorIndex 返回第 i 个作品的创作者槽位（0..CreatorCount-1）。
func CreatorIndex(i int) int { return i % CreatorCount }

// CreatorName 返回创作者槽位对应的展示名；偶数槽位为中文，奇数槽位为拉丁文，
// 覆盖 Creator 字段的 CJK/Latin 排序与搜索场景。展示名不是领域 ID，没有格式约束，
// 可以确定性生成；真正的 CanonicalCreator ID 由 seed 用正式生成器另行产生。
func CreatorName(slot int) string {
	if slot%2 == 0 {
		return fmt.Sprintf("创作者%02d", slot)
	}
	return fmt.Sprintf("Creator-%02d", slot)
}

// ProviderIndex 返回第 i 个作品的 Provider 槽位（0..4）。
func ProviderIndex(i int) int { return i % providerCount }

// ProviderID 返回 Provider 槽位对应的稳定 ID。provider.id 在 OpenAPI 中只声明为
// 普通字符串（规则声明的 Provider 命名空间，不是 domain.IDKind 前缀 ID），因此
// 可以安全使用确定性字符串。
func ProviderID(slot int) string { return fmt.Sprintf("provider-%d", slot) }

// TagSlots 返回第 i 个作品的两个标签槽位（0..39），两个公式保证不总是相同槽位。
func TagSlots(i int) (int, int) {
	a := i % tagPoolSize
	b := (i*7 + 3) % tagPoolSize
	return a, b
}

// TagName 返回标签槽位对应的展示名。Tag 是自由文本，没有 ID 格式约束。
func TagName(slot int) string { return fmt.Sprintf("tag-%02d", slot) }

// Hidden 报告第 i 个作品是否被标记为隐藏（2%）。Hidden 是 Overlay 字段能力注册表
// 中登记的 Overlay 事实（见 Documents/规范/06-查询-搜索与排序.md），权威写入路径是
// catalog.OverlayFact.Hidden 经 ApplyCatalogCandidateOverlays 落地，不是
// catalog.WorkFact.Hidden——后者会被前者对同一批作品的 UPDATE 无条件覆盖回默认值，
// 因此 seed 必须只通过 OverlayFact 设置 Hidden，这里的确定性判据两侧共用同一函数。
//
// 普通查询（不显式引用 overlay.hidden 的过滤器、搜索、排序、总数）默认隐式排除
// Hidden 作品（见规范「overlay.hidden 语义」一节）；Stats 中除
// HiddenCount/FavoriteCount 等直接描述 Hidden 本身的字段外，其余"默认可见"计数
// 字段（Visible 前缀）都已经把 Hidden 作品排除在外，probe 断言默认查询命中数时
// 必须使用这些 Visible 字段，不能直接用未排除 Hidden 的原始计数。
func Hidden(i int) bool { return i%50 == 0 }

// Favorite 报告第 i 个作品是否被标记为收藏（5%）。同样是 Overlay 权威事实。
func Favorite(i int) bool { return i%20 == 0 }

// Progress 返回第 i 个作品的阅读进度快照值；未参与该测试的作品固定为 0。
func Progress(i int) float64 {
	if i%7 == 0 {
		return float64(i%101) / 100.0
	}
	return 0
}

// MediaKind 返回第 i 个作品唯一媒体的种类（10% 为 video，其余为 image）。
func MediaKind(i int) string {
	if i%10 == 0 {
		return "video"
	}
	return "image"
}

// ContentVerified 报告第 i 个作品的媒体是否已完整内容确认（约 2/3 已确认，1/3 未确认）。
// 无论是否确认，seed 都把该媒体的 location_status 写为 present（"已发现"），因为
// content_verification_state 与 location_status 是正交语义（见
// Documents/规范/04-扫描-Catalog与任务.md），已发现但未确认内容的媒体仍然"位置可用"。
func ContentVerified(i int) bool { return i%3 != 0 }

// SourceKey 返回第 i 个作品在合成 Source 内的相对路径键。source_key 只是
// Source-derived 内部关联键（不是对外暴露的领域 ID），OpenAPI 未对其声明格式
// 约束，可以安全使用确定性字符串。
func SourceKey(i int) string { return fmt.Sprintf("stage4/work-%08d", i) }

// Stats 汇总一个给定规模 N 的语料在各个查询维度上的期望真值，供 probe 工具
// 断言真实 API 响应是否与确定性生成规则一致。除 N/HiddenCount/FavoriteCount 等
// 直接描述 Hidden/Favorite 本身的字段外，其余 Visible 前缀字段均已排除 Hidden
// 作品，匹配"普通查询默认隐式排除 Hidden"的服务端语义。
type Stats struct {
	N             int
	HiddenCount   int
	FavoriteCount int

	// Visible* 排除了 Hidden 作品，对应默认查询（未显式引用 overlay.hidden）实际
	// 会返回的命中数。
	VisibleN                      int
	VisibleSpecialCJKCount        int
	VisibleSpecialLatinCount      int
	VisibleUniqueFilenameCount    int
	VisibleVideoCount             int
	VisibleImageCount             int
	VisibleContentVerifiedCount   int
	VisibleLocatedUnverifiedCount int
	VisibleCreatorCounts          map[string]int
	VisibleProviderCounts         map[string]int
	VisibleTagCounts              map[string]int
}

// Manifest 是 testlabseed 写出的、供 testlabprobe 读取的真实生成结果：真实生成的
// 公开领域 ID（不得由 probe 硬编码或凭空猜测）与本次语料的确定性统计真值。
type Manifest struct {
	SchemaVersion      int      `json:"schemaVersion"`
	Scale              int      `json:"scale"`
	Tier               string   `json:"tier,omitempty"`
	Nonrecommended     bool     `json:"nonrecommendedScale,omitempty"`
	LibraryID          string   `json:"libraryId"`
	SourceID           string   `json:"sourceId"`
	JobID              string   `json:"jobId"`
	CreatorIDs         []string `json:"creatorIds"`
	QueryPublicationID string   `json:"queryPublicationId"`
	CatalogRevisionID  string   `json:"catalogRevisionId"`
	StageDurationMs    int64    `json:"stageDurationMs"`
	OverlayDurationMs  int64    `json:"overlayDurationMs"`
	PublishDurationMs  int64    `json:"publishDurationMs"`
	TotalDurationMs    int64    `json:"totalDurationMs"`
	Stats              Stats    `json:"stats"`
}

// LoadManifest 读取 stage4seed 产出的 manifest JSON。
func LoadManifest(path string) (Manifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ComputeStats 对 [0, n) 区间做单次遍历，计算生成规则隐含的期望统计值。
func ComputeStats(n int) Stats {
	stats := Stats{
		N:                     n,
		VisibleCreatorCounts:  make(map[string]int, CreatorCount),
		VisibleProviderCounts: make(map[string]int, providerCount),
		VisibleTagCounts:      make(map[string]int, tagPoolSize),
	}
	for i := 0; i < n; i++ {
		hidden := Hidden(i)
		if hidden {
			stats.HiddenCount++
		}
		if Favorite(i) {
			stats.FavoriteCount++
		}
		if hidden {
			continue
		}
		stats.VisibleN++
		if i%1000 == specialCJKOffset {
			stats.VisibleSpecialCJKCount++
		}
		if i%1000 == specialLatinOffset {
			stats.VisibleSpecialLatinCount++
		}
		if i%500 == uniqueFilenameOffset {
			stats.VisibleUniqueFilenameCount++
		}
		if MediaKind(i) == "video" {
			stats.VisibleVideoCount++
		} else {
			stats.VisibleImageCount++
		}
		if ContentVerified(i) {
			stats.VisibleContentVerifiedCount++
		} else {
			stats.VisibleLocatedUnverifiedCount++
		}
		stats.VisibleCreatorCounts[CreatorName(CreatorIndex(i))]++
		stats.VisibleProviderCounts[ProviderID(ProviderIndex(i))]++
		a, b := TagSlots(i)
		stats.VisibleTagCounts[TagName(a)]++
		if b != a {
			stats.VisibleTagCounts[TagName(b)]++
		}
	}
	return stats
}
