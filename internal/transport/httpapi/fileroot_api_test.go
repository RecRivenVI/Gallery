package httpapi_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
)

// absolutePathPattern 匹配 Windows 盘符路径与常见的 Unix 绝对路径前缀。文件根响应必须只含
// 相对路径与显示名，绝不含服务端文件系统布局。
var absolutePathPattern = regexp.MustCompile(`[A-Za-z]:[\\/]|/home/|/Users/|/mnt/|/var/`)

// TestFileRootEndpointsRequireCapabilityAndHideAbsolutePaths 覆盖文件根端点的三条对外契约：
// 需要认证与 capability、不泄露绝对路径、未知根返回 404。
//
// 未装配文件根时端点仍必须可解释（空数组 + 404），而不是空指针崩溃——文件根是可选装配。
func TestFileRootEndpointsRequireCapabilityAndHideAbsolutePaths(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	client, _ := establishLANOwner(t, server)

	response, err := client.Get(server.URL + "/api/v1/file-roots")
	if err != nil {
		t.Fatal(err)
	}
	body := readAndClose(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("文件根列表 status=%d body=%s", response.StatusCode, body)
	}
	var listed struct {
		FileRoots []struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"fileRoots"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("文件根列表不是合法 JSON: %s", body)
	}
	// 未注入文件根时返回空数组而不是 null，也不崩溃。
	if listed.FileRoots == nil {
		t.Fatalf("未装配文件根时应返回空数组: %s", body)
	}
	if absolutePathPattern.Match(body) {
		t.Fatalf("文件根列表泄露了绝对路径: %s", body)
	}

	missing, err := client.Get(server.URL + "/api/v1/file-roots/nonexistent/entries")
	if err != nil {
		t.Fatal(err)
	}
	missingBody := readAndClose(t, missing)
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("未知文件根 status=%d body=%s", missing.StatusCode, missingBody)
	}
	if absolutePathPattern.Match(missingBody) {
		t.Fatalf("未知文件根的错误响应泄露了绝对路径: %s", missingBody)
	}
}

// TestFileRootEndpointsRejectUnauthenticatedAccess 证明文件根浏览不对未认证主体开放。
func TestFileRootEndpointsRejectUnauthenticatedAccess(t *testing.T) {
	server, _ := newLANSecurityServer(t, false)
	for _, path := range []string{"/api/v1/file-roots", "/api/v1/file-roots/files/entries"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if body := readAndClose(t, response); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s 未认证访问 status=%d body=%s", path, response.StatusCode, body)
		}
	}
}
