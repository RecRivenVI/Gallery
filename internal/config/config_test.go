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

func TestLANIsNotSilentlyEnabledBeforeOwnerGate(t *testing.T) {
	_, err := config.Parse([]string{"--app-root", t.TempDir(), "--mode", "lan"})
	if err == nil {
		t.Fatal("未实现 Owner 初始化时启用了 LAN")
	}
}
