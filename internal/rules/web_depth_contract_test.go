package rules_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

var webRuleContainerDepthPattern = regexp.MustCompile(`RULE_CONTAINER_DEPTH_LIMIT\s*=\s*([0-9]+)\s*;`)
var webRulePackageMaxBytesPattern = regexp.MustCompile(`RULE_PACKAGE_MAX_BYTES\s*=\s*([0-9]+)\s*\*\s*([0-9]+)\s*\*\s*([0-9]+)\s*;`)

// TestWebRuleContainerDepthMatchesBackend 防止管理端结构化编辑器与后端规则管线
// 使用不同的容器深度边界。前端守卫只负责避免递归挂载超深草稿，服务端仍是最终权威。
func TestWebRuleContainerDepthMatchesBackend(t *testing.T) {
	path := filepath.Join("..", "..", "web", "src", "manage", "rules", "RuleStructuredFields.tsx")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取前端规则结构字段失败: %v", err)
	}
	match := webRuleContainerDepthPattern.FindSubmatch(contents)
	if len(match) != 2 {
		t.Fatal("前端缺少可核对的 RULE_CONTAINER_DEPTH_LIMIT 常量")
	}
	limit, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatalf("解析前端规则容器深度失败: %v", err)
	}
	if limit != rules.MaxRuleNestingDepth {
		t.Fatalf("前端规则容器深度=%d，后端=%d", limit, rules.MaxRuleNestingDepth)
	}
}

// TestWebRulePackageBytesMatchesBackend 防止前端在进入 Lossless JSON、Schema/AJV
// 或网络提交前使用与服务端不同的规则内容字节边界。
func TestWebRulePackageBytesMatchesBackend(t *testing.T) {
	path := filepath.Join("..", "..", "web", "src", "manage", "rules", "limits.ts")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取前端规则限制失败: %v", err)
	}
	match := webRulePackageMaxBytesPattern.FindSubmatch(contents)
	if len(match) != 4 {
		t.Fatal("前端缺少可核对的 RULE_PACKAGE_MAX_BYTES 常量")
	}
	limit := 1
	for _, raw := range match[1:] {
		factor, parseErr := strconv.Atoi(string(raw))
		if parseErr != nil {
			t.Fatalf("解析前端规则字节上限失败: %v", parseErr)
		}
		limit *= factor
	}
	if limit != rules.MaxRulePackageBytes {
		t.Fatalf("前端规则字节上限=%d，后端=%d", limit, rules.MaxRulePackageBytes)
	}
}
