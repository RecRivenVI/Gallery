package auth_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/RecRivenVI/gallery/internal/auth"
)

var webCapabilityPattern = regexp.MustCompile(`'([a-z]+\.[a-z]+)'`)

// TestWebCapabilityVocabularyMatchesBackend 阻止前端再次发明后端并不存在的 capability
// 名。此前 Web 使用了 overlay.write / media.verify / library.manage / bindings.resolve /
// jobs.cancel / jobs.retry 六个不存在的名字，它们永远不出现在 effectiveCapabilities 中，
// 因此 Overlay 编辑、任务取消与重试、Library 创建、Source 登记、按需内容确认与全部治理
// 动作对任何主体都不渲染；而 mock 浏览器套件把同样的错误名字写进合成 bootstrap，使测试
// 自证通过。本测试以后端词表为唯一事实源逐项比对前端副本。
func TestWebCapabilityVocabularyMatchesBackend(t *testing.T) {
	path := filepath.Join("..", "..", "web", "src", "auth", "capabilities.ts")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取前端 capability 词表失败: %v", err)
	}
	body := string(contents)
	start := len("export const CAPABILITIES = [")
	if index := indexOf(body, "export const CAPABILITIES = ["); index >= 0 {
		body = body[index+start:]
	} else {
		t.Fatal("前端 capability 词表缺少 CAPABILITIES 常量")
	}
	end := indexOf(body, "] as const;")
	if end < 0 {
		t.Fatal("前端 capability 词表 CAPABILITIES 常量未正确闭合")
	}
	var web []string
	for _, match := range webCapabilityPattern.FindAllStringSubmatch(body[:end], -1) {
		web = append(web, match[1])
	}
	backend := append([]string(nil), auth.PersonalOwnerCapabilities...)
	sort.Strings(backend)
	sort.Strings(web)
	if len(web) != len(backend) {
		t.Fatalf("前端 capability 数量 %d 与后端 %d 不一致\n前端=%v\n后端=%v", len(web), len(backend), web, backend)
	}
	for index := range backend {
		if web[index] != backend[index] {
			t.Fatalf("capability 词表第 %d 项不一致: 前端=%q 后端=%q", index, web[index], backend[index])
		}
	}
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
