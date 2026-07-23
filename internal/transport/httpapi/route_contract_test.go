package httpapi_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	contractapi "github.com/RecRivenVI/gallery/internal/contract/api"
)

var (
	muxRoutePattern      = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/v1/[^"]*)"`)
	openAPIPathPattern   = regexp.MustCompile(`^  (/api/v1/\S*):\s*$`)
	openAPIMethodPattern = regexp.MustCompile(`^    (get|post|put|patch|delete|head):\s*$`)
)

// TestOpenAPIOperationsMatchRegisteredRoutes 把 OpenAPI 的 method+path 集合与 server.go
// 实际注册的路由集合逐项比对。此前 deleteRulePackage 被声明在
// /api/v1/rule-packages/{packageId}/deprecate，而实现注册在
// /api/v1/rule-packages/{packageId}，生成的 Go 与 TypeScript 客户端都固化了错误路径、
// 调用必得 405；既有契约测试只校验 info.version 与错误码枚举，因此无法发现这类漂移。
func TestOpenAPIOperationsMatchRegisteredRoutes(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取路由注册源失败: %v", err)
	}
	registered := map[string]bool{}
	for _, match := range muxRoutePattern.FindAllStringSubmatch(string(source), -1) {
		registered[match[1]+" "+match[2]] = true
	}
	if len(registered) == 0 {
		t.Fatal("未能从 server.go 解析到任何 /api/v1 路由，测试本身已失效")
	}

	declared := map[string]bool{}
	currentPath := ""
	for _, line := range strings.Split(string(contractapi.OpenAPISpec()), "\n") {
		line = strings.TrimRight(line, "\r")
		if match := openAPIPathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.TrimSpace(line) != "" {
			currentPath = ""
		}
		if currentPath == "" {
			continue
		}
		if match := openAPIMethodPattern.FindStringSubmatch(line); match != nil {
			declared[strings.ToUpper(match[1])+" "+currentPath] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("未能从 OpenAPI 解析到任何 /api/v1 操作，测试本身已失效")
	}

	var onlyDeclared, onlyRegistered []string
	for operation := range declared {
		if !registered[operation] {
			onlyDeclared = append(onlyDeclared, operation)
		}
	}
	for operation := range registered {
		if !declared[operation] {
			onlyRegistered = append(onlyRegistered, operation)
		}
	}
	sort.Strings(onlyDeclared)
	sort.Strings(onlyRegistered)
	if len(onlyDeclared) != 0 {
		t.Errorf("OpenAPI 声明但服务端未注册的操作: %v", onlyDeclared)
	}
	if len(onlyRegistered) != 0 {
		t.Errorf("服务端注册但 OpenAPI 未声明的操作: %v", onlyRegistered)
	}
}
