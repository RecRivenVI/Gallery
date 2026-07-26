// Package legacyrules 把真实旧配置文件转换成 testlab 可直接消费的逐平台规则包索引。
//
// 它是 tools/testlab 里**唯一**导入 internal/rules 的包，只被 testlabrulesimport 链接。
// 这样做的理由是边界而不是洁癖：验证入口（testlabprobe）必须只经公开契约驱动被测系统，
// 让它间接链接 internal/* 会削弱那条性质；而转换本身不是「驱动被测系统」，它是把用户的
// 旧配置一次性变成规则包的离线步骤。
//
// 转换产物取代了此前手写的 fixtures/rules/<来源>/bounded-subdir-v1.json：手写夹具与真实
// 配置之间没有任何机制保证同步，一旦漂移，「规则验证通过」证明的就只是夹具自洽，而不是
// 用户真实配置可用。
//
// 本包不读取、不输出、不落盘任何真实路径以外必需的内容：真实根路径只写进本地索引制品
// （由 ruleindex.Save 拒绝写入 Git 工作树），报告侧一律只用平台代号。
package legacyrules

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/rules/legacy"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
)

// Bundle 是一次转换的完整产物：索引 + 按平台代号索引的规则包内容。
type Bundle struct {
	Index    ruleindex.Index
	Packages map[string]json.RawMessage
}

// ConvertFile 读取给定旧配置文件并转换。
//
// path 必须由调用方显式传入（命令行参数）：本包不猜测、不扫描磁盘、不内置任何默认路径。
func ConvertFile(path string) (Bundle, error) {
	if path == "" {
		return Bundle{}, fmt.Errorf("必须显式指定旧配置文件路径，不得猜测或扫描磁盘")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Bundle{}, fmt.Errorf("读取旧配置失败：%w", err)
	}
	return Convert(body)
}

// Convert 转换一份旧配置内容。
func Convert(body []byte) (Bundle, error) {
	platformIDs, legacySchemaVersion, err := enabledPlatforms(body)
	if err != nil {
		return Bundle{}, err
	}
	if len(platformIDs) == 0 {
		return Bundle{}, fmt.Errorf("旧配置没有任何启用平台，无可转换内容")
	}
	ruleSetIDs := make(map[string]string, len(platformIDs))
	for _, id := range platformIDs {
		ruleSetIDs[id] = ruleindex.RuleSetID(id)
	}

	result, err := legacy.Convert(body, ruleSetIDs)
	if err != nil {
		return Bundle{}, fmt.Errorf("转换旧配置失败：%w", err)
	}

	bundle := Bundle{
		Index: ruleindex.Index{
			LegacySchemaVersion: legacySchemaVersion,
			FileRootCount:       len(result.FileRoots),
			UnconvertedByField:  bucketUnconverted(result.Unconverted),
		},
		Packages: map[string]json.RawMessage{},
	}

	ids := make([]string, 0, len(result.Packages))
	for id := range result.Packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, platformID := range ids {
		encoded := result.Packages[platformID]
		// 「能被生产编译器接受」是转换正确性的判据：转换器不自己实现一套校验，这里也不
		// 放宽。编译失败必须让导入整体失败，而不是产出一个到扫描时才炸的规则包。
		compiled, err := rules.CompilePackage(encoded)
		if err != nil {
			return Bundle{}, fmt.Errorf("平台 %s 的规则包无法编译：%w", ruleindex.Code(platformID), err)
		}
		if _, _, _, err := rules.CompileBinding(compiled, []byte(`{}`)); err != nil {
			return Bundle{}, fmt.Errorf("平台 %s 的规则绑定无法编译：%w", ruleindex.Code(platformID), err)
		}
		root := result.SourceRoots[platformID]
		if root == "" {
			return Bundle{}, fmt.Errorf("平台 %s 缺少只读根路径", ruleindex.Code(platformID))
		}
		code := ruleindex.Code(platformID)
		bundle.Index.Entries = append(bundle.Index.Entries, ruleindex.Entry{
			PlatformCode: code, RuleSetID: ruleSetIDs[platformID],
			SourceRoot: root, PrimitiveCount: primitiveCount(encoded),
		})
		bundle.Packages[code] = encoded
	}
	return bundle, nil
}

// enabledPlatforms 只解析出「哪些平台被启用」这一件事，用于派生 rule_set_id。
// 它刻意不复用 legacy.Config：那是转换器的输入模型，这里只需要一个最小探针。
func enabledPlatforms(body []byte) ([]string, int, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
		Platforms     []struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, 0, fmt.Errorf("旧配置不是合法 JSON：%w", err)
	}
	ids := make([]string, 0, len(probe.Platforms))
	for _, platform := range probe.Platforms {
		if platform.Enabled {
			ids = append(ids, platform.ID)
		}
	}
	return ids, probe.SchemaVersion, nil
}

// bucketUnconverted 把未转换登记按字段名前两段聚合。
//
// 只取前两段是刻意的：`cover.candidates.<候选ID>` 的第三段是用户配置里的自定义 ID，
// 属于用户配置内容，不该出现在可能被提交的产物里；前两段是旧配置 schema 的固定结构名，
// 足以说明「哪一类语义没转过来」。
func bucketUnconverted(notes []legacy.Note) map[string]int {
	buckets := map[string]int{}
	for _, note := range notes {
		buckets[fieldBucket(note.Field)]++
	}
	return buckets
}

func fieldBucket(field string) string {
	segments := strings.Split(field, ".")
	if len(segments) <= 2 {
		return field
	}
	return strings.Join(segments[:2], ".")
}

func primitiveCount(encoded json.RawMessage) int {
	var probe struct {
		Primitives []json.RawMessage `json:"primitives"`
	}
	if json.Unmarshal(encoded, &probe) != nil {
		return 0
	}
	return len(probe.Primitives)
}
