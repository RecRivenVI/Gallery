package tooldiscovery_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/tooldiscovery"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type probeController struct {
	stdout  string
	stderr  string
	waitErr error
	started []ports.Command
}

func (c *probeController) Start(_ context.Context, command ports.Command) (ports.Process, error) {
	c.started = append(c.started, command)
	if command.Stdout != nil {
		_, _ = io.WriteString(command.Stdout, c.stdout)
	}
	if command.Stderr != nil {
		_, _ = io.WriteString(command.Stderr, c.stderr)
	}
	return probeProcess{waitErr: c.waitErr}, nil
}

type probeProcess struct{ waitErr error }

func (p probeProcess) Wait() error { return p.waitErr }
func (probeProcess) Kill() error   { return nil }

func writeTool(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.bin")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(digest[:])
}

func TestDiscoveryVerifiesPinsAndReturnsPathFreeCapabilities(t *testing.T) {
	path, digest := writeTool(t, "pinned binary")
	controller := &probeController{stdout: "ffprobe version 7.1.1 Copyright test\n"}
	discovery, err := tooldiscovery.New(context.Background(), []tooldiscovery.Declaration{{
		ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: strings.ToUpper(digest),
	}}, controller)
	if err != nil {
		t.Fatal(err)
	}
	if !discovery.Available("ffprobe") || discovery.Available("ffmpeg") {
		t.Fatal("工具可用性与声明不一致")
	}
	capabilities := discovery.Capabilities()
	if len(capabilities) != 1 || capabilities[0].ID != "ffprobe" ||
		capabilities[0].Version != "7.1.1" || capabilities[0].SHA256 != digest {
		t.Fatalf("能力报告不正确: %+v", capabilities)
	}
	if strings.Contains(strings.Join([]string{capabilities[0].ID, capabilities[0].Version, capabilities[0].SHA256}, " "), path) {
		t.Fatal("能力报告泄漏了可执行文件路径")
	}
	if len(controller.started) != 1 || len(controller.started[0].Args) != 1 || controller.started[0].Args[0] != "-version" {
		t.Fatalf("版本探测没有使用固定参数数组: %+v", controller.started)
	}

	command, err := discovery.Resolve(context.Background(), "ffprobe", []string{"-show_format", "input.bin"}, "working")
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != path || command.Dir != "working" || strings.Join(command.Args, " ") != "-show_format input.bin" {
		t.Fatalf("解析结果不正确: %+v", command)
	}
}

func TestDiscoveryRejectsHashVersionAndOutputViolations(t *testing.T) {
	path, digest := writeTool(t, "pinned binary")
	tests := []struct {
		name        string
		declaration tooldiscovery.Declaration
		stdout      string
	}{
		{
			name: "摘要不匹配",
			declaration: tooldiscovery.Declaration{ID: "ffprobe", Path: path, Version: "7.1.1",
				SHA256: strings.Repeat("0", 64)},
			stdout: "ffprobe version 7.1.1\n",
		},
		{
			name:        "版本不匹配",
			declaration: tooldiscovery.Declaration{ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: digest},
			stdout:      "ffprobe version 7.2.0\n",
		},
		{
			name:        "首行身份不匹配",
			declaration: tooldiscovery.Declaration{ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: digest},
			stdout:      "ffmpeg version 7.1.1\n",
		},
		{
			name:        "版本输出越界",
			declaration: tooldiscovery.Declaration{ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: digest},
			stdout:      strings.Repeat("x", 70<<10),
		},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			_, err := tooldiscovery.New(context.Background(), []tooldiscovery.Declaration{item.declaration},
				&probeController{stdout: item.stdout})
			var structured *fault.Error
			if !errors.As(err, &structured) || structured.Code != fault.CodeExternalToolUnavailable {
				t.Fatalf("错误 = %v，期望 EXTERNAL_TOOL_UNAVAILABLE", err)
			}
		})
	}
}

func TestResolveRejectsUnconfiguredAndReplacedTool(t *testing.T) {
	path, digest := writeTool(t, "original")
	discovery, err := tooldiscovery.New(context.Background(), []tooldiscovery.Declaration{{
		ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: digest,
	}}, &probeController{stdout: "ffprobe version 7.1.1\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.Resolve(context.Background(), "ffmpeg", nil, ""); !hasUnavailableCode(err) {
		t.Fatalf("未配置工具错误 = %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.Resolve(context.Background(), "ffprobe", nil, ""); !hasUnavailableCode(err) {
		t.Fatalf("替换后的工具错误 = %v", err)
	}
}

func TestDiscoveryErrorsDoNotExposeConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "missing-ffprobe.exe")
	_, err := tooldiscovery.New(context.Background(), []tooldiscovery.Declaration{{
		ID: "ffprobe", Path: path, Version: "7.1.1", SHA256: strings.Repeat("0", 64),
	}}, &probeController{})
	if err == nil {
		t.Fatal("不存在的工具未被拒绝")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("错误泄漏了配置路径: %v", err)
	}
}

func hasUnavailableCode(err error) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeExternalToolUnavailable
}
