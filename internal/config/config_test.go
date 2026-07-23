package config_test

import (
	"errors"
	"path/filepath"
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
