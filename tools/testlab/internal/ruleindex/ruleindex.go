// Package ruleindex 定义「转换产物索引」——一次旧配置转换所产出的逐平台规则包及其
// 只读根路径的清单——的读写与脱敏代号规则。
//
// 本包只依赖标准库。它被 testlabprobe 链接，而 probe 的既定边界是「只导入
// api 与标准库、从不导入 internal/*」：转换本身（导入
// internal/rules/legacy）由独立的 testlabrulesimport 命令完成，probe 只消费它的产物。
// 这样「验证入口消费转换产物而不是手写夹具」与「验证入口不持有特权访问」两条同时成立。
//
// 索引文件里含有真实物理根路径（登记 Source 必须用它），因此它是**只存在于授权测试根
// 的本地制品**，绝不进入仓库，也绝不进入任何报告。Save 会主动拒绝写进任何 Git 工作树。
package ruleindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersion 随索引结构变化递增。
const SchemaVersion = 1

// FileName 是索引在输出目录中的固定文件名。
const FileName = "rule-index.json"

// Entry 是一个平台的转换产物登记。
type Entry struct {
	// PlatformCode 是写进报告的唯一标识：由平台 ID 派生的稳定代号，不透露平台名本身。
	PlatformCode string `json:"platformCode"`
	// RuleSetID 是该平台规则包的 rule_set_id。
	RuleSetID string `json:"ruleSetId"`
	// PackageFile 是规则包文件名，相对于索引文件所在目录。
	PackageFile string `json:"packageFile"`
	// SourceRoot 是该平台的真实只读根。**本地制品字段**，不得进入报告或仓库。
	SourceRoot string `json:"sourceRoot"`
	// PrimitiveCount 是规则包中的原语数量，用于在报告中给出转换规模而不泄露内容。
	PrimitiveCount int `json:"primitiveCount"`
}

// Index 是一次转换的完整产物清单。
type Index struct {
	SchemaVersion int    `json:"schemaVersion"`
	GeneratedAt   string `json:"generatedAt"`
	// LegacySchemaVersion 是被转换的旧配置声明的 schema_version。
	LegacySchemaVersion int `json:"legacySchemaVersion"`
	// FileRootCount 是启用的文件根数量（只记数量，不记路径）。
	FileRootCount int     `json:"fileRootCount"`
	Entries       []Entry `json:"entries"`
	// UnconvertedByField 按旧配置字段名聚合未转换登记的条数。字段名取前两段
	// （例如 `cover.candidates`），避免把配置里的自定义 ID 带出来。
	UnconvertedByField map[string]int `json:"unconvertedByField"`
}

// Code 返回平台 ID 的稳定脱敏代号。
//
// 代号而不是平台名：报告可能被提交或对外展示，而「本机上存在哪些来源」本身不该由报告
// 泄露。同一平台 ID 在任何一次运行中都得到同一个代号，因此仍然可以跨运行对照。
func Code(platformID string) string {
	sum := sha256.Sum256([]byte(platformID))
	return "p-" + hex.EncodeToString(sum[:])[:8]
}

// RuleSetID 从平台 ID 派生一个稳定、合法的 rule_set_id（`^rset_[0-9a-f-]{36}$`）。
//
// 确定性是刻意的：同一平台重复转换必须得到同一个 rule_set_id，否则每次导入都会被
// 当成一个全新的规则集，无法复用已发布版本，也无法证明「续跑没有重做」。
func RuleSetID(platformID string) string {
	sum := sha256.Sum256([]byte("gallery-testlab-ruleset/" + platformID))
	raw := hex.EncodeToString(sum[:])
	return fmt.Sprintf("rset_%s-%s-%s-%s-%s", raw[0:8], raw[8:12], raw[12:16], raw[16:20], raw[20:32])
}

// Find 按脱敏代号查找登记。
func (i Index) Find(code string) (Entry, bool) {
	for _, entry := range i.Entries {
		if entry.PlatformCode == code {
			return entry, true
		}
	}
	return Entry{}, false
}

// Codes 返回全部平台代号（已排序），可安全打印。
func (i Index) Codes() []string {
	codes := make([]string, 0, len(i.Entries))
	for _, entry := range i.Entries {
		codes = append(codes, entry.PlatformCode)
	}
	sort.Strings(codes)
	return codes
}

// EnsureOutsideRepository 拒绝把含有真实路径的制品写进任何 Git 工作树。
//
// 这不是洁癖：索引文件逐条记录真实平台根路径，一旦落在仓库内就随时可能被 `git add -A`
// 带进提交。目录不存在时向上找最近的已存在祖先再判定。
func EnsureOutsideRepository(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	probe := absolute
	for {
		if _, statErr := os.Stat(filepath.Join(probe, ".git")); statErr == nil {
			return fmt.Errorf("拒绝把含真实路径的转换产物写入 Git 工作树（检出到 %s 下存在 .git）；请写入授权测试根", probe)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

// Save 把索引与逐平台规则包写入 dir。packages 按平台代号索引。
func Save(index Index, packages map[string]json.RawMessage, dir string) error {
	if err := EnsureOutsideRepository(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	index.SchemaVersion = SchemaVersion
	index.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	for i := range index.Entries {
		entry := &index.Entries[i]
		body, ok := packages[entry.PlatformCode]
		if !ok {
			return fmt.Errorf("平台代号 %s 缺少规则包内容", entry.PlatformCode)
		}
		entry.PackageFile = entry.PlatformCode + ".rules.json"
		var pretty json.RawMessage = body
		if indented, err := indentJSON(body); err == nil {
			pretty = indented
		}
		if err := os.WriteFile(filepath.Join(dir, entry.PackageFile), pretty, 0o600); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), encoded, 0o600)
}

func indentJSON(body []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return json.MarshalIndent(value, "", "  ")
}

// Load 读取索引，并返回索引文件所在目录（规则包文件相对于它）。
func Load(path string) (Index, string, error) {
	if path == "" {
		return Index{}, "", fmt.Errorf("必须显式指定转换产物索引路径，不得猜测或扫描磁盘")
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, FileName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, "", fmt.Errorf("读取转换产物索引失败（%s）：%w；请先运行 testlabrulesimport", path, err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, "", fmt.Errorf("解析转换产物索引失败：%w", err)
	}
	if index.SchemaVersion != SchemaVersion {
		return Index{}, "", fmt.Errorf("转换产物索引 schemaVersion = %d，本工具只支持 %d", index.SchemaVersion, SchemaVersion)
	}
	if len(index.Entries) == 0 {
		return Index{}, "", fmt.Errorf("转换产物索引没有任何平台条目")
	}
	return index, filepath.Dir(path), nil
}

// LoadPackage 读取该平台的规则包，返回可直接作为 RuleVersionCreateRequest.Package
// 提交的通用 JSON 对象。
func (e Entry) LoadPackage(dir string) (map[string]any, error) {
	if e.PackageFile == "" || strings.ContainsAny(e.PackageFile, `/\`) {
		return nil, fmt.Errorf("非法的规则包文件名 %q", e.PackageFile)
	}
	data, err := os.ReadFile(filepath.Join(dir, e.PackageFile))
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}
