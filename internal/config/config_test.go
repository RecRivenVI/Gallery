package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

func TestPersonalModeOnlyAcceptsLoopback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if _, err := config.Parse([]string{"--app-root", root, "--listen", "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	_, err := config.Parse([]string{"--app-root", root, "--listen", "0.0.0.0:8080"})
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeConfigInvalid {
		t.Fatalf("非 loopback 错误 = %v", err)
	}
}

func TestExternalToolDeclarationsRequireCompleteExplicitPins(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(root, "ffprobe.exe")
	digest := strings.Repeat("a", 64)
	cfg, err := config.Parse([]string{
		"--app-root", filepath.Join(root, "app"),
		"--external-tool-path", "ffprobe=" + toolPath,
		"--external-tool-version", "ffprobe=7.1.1",
		"--external-tool-sha256", "ffprobe=" + strings.ToUpper(digest),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExternalTools) != 1 {
		t.Fatalf("工具声明数量 = %d", len(cfg.ExternalTools))
	}
	tool := cfg.ExternalTools[0]
	if tool.ID != "ffprobe" || tool.Path != toolPath || tool.Version != "7.1.1" || tool.SHA256 != digest {
		t.Fatalf("工具声明未精确保留/规范化: %+v", tool)
	}

	for name, args := range map[string][]string{
		"缺少摘要": {
			"--external-tool-path", "ffprobe=" + toolPath,
			"--external-tool-version", "ffprobe=7.1.1",
		},
		"相对路径": {
			"--external-tool-path", "ffprobe=ffprobe.exe",
			"--external-tool-version", "ffprobe=7.1.1",
			"--external-tool-sha256", "ffprobe=" + digest,
		},
		"未知工具": {
			"--external-tool-path", "custom=" + toolPath,
			"--external-tool-version", "custom=1.0",
			"--external-tool-sha256", "custom=" + digest,
		},
		"非法版本": {
			"--external-tool-path", "ffprobe=" + toolPath,
			"--external-tool-version", "ffprobe=7.1 with spaces",
			"--external-tool-sha256", "ffprobe=" + digest,
		},
		"非法摘要": {
			"--external-tool-path", "ffprobe=" + toolPath,
			"--external-tool-version", "ffprobe=7.1.1",
			"--external-tool-sha256", "ffprobe=not-a-digest",
		},
		"重复路径": {
			"--external-tool-path", "ffprobe=" + toolPath,
			"--external-tool-path", "ffprobe=" + toolPath,
			"--external-tool-version", "ffprobe=7.1.1",
			"--external-tool-sha256", "ffprobe=" + digest,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse(append([]string{"--app-root", filepath.Join(root, "app-"+name)}, args...))
			var structured *fault.Error
			if !errors.As(err, &structured) || structured.Code != fault.CodeConfigInvalid {
				t.Fatalf("错误 = %v，期望 CONFIG_INVALID", err)
			}
		})
	}
}

func TestLANAcceptsLoopbackInitializationAndPrivateListen(t *testing.T) {
	if _, err := config.Parse([]string{"--app-root", t.TempDir(), "--mode", "lan"}); err != nil {
		t.Fatalf("LAN loopback 初始化监听被拒绝: %v", err)
	}
	if _, err := config.Parse([]string{"--app-root", t.TempDir(), "--mode", "lan", "--listen", "192.168.1.20:8080"}); err != nil {
		t.Fatalf("LAN 私有地址被拒绝: %v", err)
	}
	for _, listen := range []string{"0.0.0.0:8080", "8.8.8.8:8080", "[::]:8080"} {
		if _, err := config.Parse([]string{"--app-root", t.TempDir(), "--mode", "lan", "--listen", listen}); err == nil {
			t.Fatalf("LAN 接受了非私有/未指定地址: %s", listen)
		}
	}
}
