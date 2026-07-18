package rules

import (
	"context"
	"embed"
	"fmt"
	"path"
	"sort"
)

//go:embed testdata/examples/*.json
var builtInExampleFiles embed.FS

// BuiltInRuleExample 是可供规则编辑器和黄金测试使用的脱敏合成规则样本。
// PackageJSON 来自仓库内嵌资源，不接受服务器文件路径，也不写入 Source。
type BuiltInRuleExample struct {
	ID          string
	Name        string
	Category    string
	PackageJSON []byte
}

var builtInExampleMetadata = map[string]struct {
	name     string
	category string
}{
	"author-work-media": {name: "作者—作品—媒体层级", category: "hierarchical"},
	"direct-work":       {name: "作品目录直接包含元数据和多媒体", category: "direct"},
	"messy-structure":   {name: "混乱或不完整结构", category: "messy"},
}

// BuiltInRuleExamples 返回排序稳定、内容独立拷贝的内置示例。
func BuiltInRuleExamples() []BuiltInRuleExample {
	ids := make([]string, 0, len(builtInExampleMetadata))
	for id := range builtInExampleMetadata {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]BuiltInRuleExample, 0, len(ids))
	for _, id := range ids {
		example, ok := BuiltInRuleExampleByID(id)
		if ok {
			result = append(result, example)
		}
	}
	return result
}

// BuiltInRuleExampleByID 读取单个内嵌示例。
func BuiltInRuleExampleByID(id string) (BuiltInRuleExample, bool) {
	metadata, ok := builtInExampleMetadata[id]
	if !ok {
		return BuiltInRuleExample{}, false
	}
	content, err := builtInExampleFiles.ReadFile(path.Join("testdata", "examples", id+".json"))
	if err != nil {
		return BuiltInRuleExample{}, false
	}
	return BuiltInRuleExample{ID: id, Name: metadata.name, Category: metadata.category, PackageJSON: append([]byte(nil), content...)}, true
}

// BuiltInRuleExampleSample 提供不会暴露宿主文件系统的默认 Dry Run 输入。
func BuiltInRuleExampleSample(id string) (DryRunInput, bool) {
	switch id {
	case "author-work-media":
		return DryRunInput{
			Path:     "author/work",
			Metadata: map[string]any{"author": map[string]any{"name": "示例作者"}},
			Files:    []DryRunFile{{Path: "image.jpg", Size: 1024}},
		}, true
	case "direct-work":
		return DryRunInput{
			Path:     "work",
			Metadata: map[string]any{"title": "示例作品"},
			Files:    []DryRunFile{{Path: "cover.webp", Size: 2048}},
		}, true
	case "messy-structure":
		return DryRunInput{
			Path:     "flat",
			Metadata: map[string]any{"post": map[string]any{"title": "回退标题"}},
			Files: []DryRunFile{
				{Path: "cover.jpg", Size: 1024},
				{Path: "image-01.jpg", Size: 2048},
				{Path: "attachment.zip", Size: 4096},
			},
		}, true
	default:
		return DryRunInput{}, false
	}
}

// RunBuiltInRuleExample 执行内嵌示例的受限 Dry Run。它只接受请求体里的合成
// Sample，不读取示例以外的本地路径。
func RunBuiltInRuleExample(ctx context.Context, id string, parameters []byte, sample *DryRunInput) (DryRunResult, error) {
	example, ok := BuiltInRuleExampleByID(id)
	if !ok {
		return DryRunResult{}, fmt.Errorf("内置规则示例不存在: %s", id)
	}
	if sample == nil {
		defaultSample, ok := BuiltInRuleExampleSample(id)
		if !ok {
			return DryRunResult{}, fmt.Errorf("内置规则示例缺少默认样本: %s", id)
		}
		sample = &defaultSample
	}
	lifecycle, err := NewLifecycle()
	if err != nil {
		return DryRunResult{}, err
	}
	return lifecycle.DryRun(ctx, example.PackageJSON, parameters, *sample)
}
