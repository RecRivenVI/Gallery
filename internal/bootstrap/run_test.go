package bootstrap_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/bootstrap"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/api"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
)

func TestRunnableGallerydUsesAppDirsAndLeavesSyntheticSourceUnchanged(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(source, "media.bin")
	content := []byte("synthetic read-only media")
	if err := os.WriteFile(sentinel, content, 0o600); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(content)
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs, SourceRoots: []string{source}}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(ctx, cfg, logger) }()

	descriptorPath := filepath.Join(dirs.Runtime, "galleryd.json")
	runtimeDescriptor := waitForDescriptor(t, descriptorPath)
	client, err := api.NewClientWithResponses("http://" + runtimeDescriptor.Address)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response, err := client.GetHealthWithResponse(context.Background())
	if err != nil || response.JSON200 == nil {
		cancel()
		t.Fatalf("运行中的 galleryd health 失败: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("galleryd 未在期限内优雅停止")
	}

	afterContent, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	after := sha256.Sum256(afterContent)
	if before != after {
		t.Fatal("合成 Source 在启动/停止后发生变化")
	}
	for _, name := range []string{"control.db", "catalog.db"} {
		if _, err := os.Stat(filepath.Join(dirs.Data, name)); err != nil {
			t.Fatalf("AppDirs 数据库未创建: %v", err)
		}
	}
	if _, err := os.Stat(descriptorPath); !os.IsNotExist(err) {
		t.Fatal("停止后 runtime descriptor 未清理")
	}
}

func TestOverlapFailsBeforeDatabaseInitialization(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	source := filepath.Join(dirs.Data, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs, SourceRoots: []string{source}}
	err := bootstrap.Run(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("重叠 Source 启动成功")
	}
	if _, err := os.Stat(filepath.Join(dirs.Data, "control.db")); !os.IsNotExist(err) {
		t.Fatal("重叠守卫失败前已初始化数据库")
	}
}

func waitForDescriptor(t *testing.T, path string) descriptor.Descriptor {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			var value descriptor.Descriptor
			if err := json.Unmarshal(content, &value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("未等待到 runtime descriptor")
	return descriptor.Descriptor{}
}
