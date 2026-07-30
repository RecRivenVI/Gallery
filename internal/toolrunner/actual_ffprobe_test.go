package toolrunner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
)

// 这组用例默认关闭。它只接受显式绝对 ffprobe 路径，不从 PATH 猜测工具，也不改变生产 Resolver
// 仍为 nil 的 fail-closed 行为。发布门禁可用本机或受控制品中的真实 ffprobe 补充“实际工具进程”证据；
// 普通单元测试仍只依赖仓库自己的测试二进制。
const actualFFprobePathEnv = "GALLERY_TEST_FFPROBE_PATH"

type explicitFFprobeResolver struct {
	path string
}

func (r explicitFFprobeResolver) Available(toolID string) bool {
	return toolID == "ffprobe" && r.path != ""
}

func (r explicitFFprobeResolver) Resolve(_ context.Context, toolID string, args []string, workingDir string) (ports.Command, error) {
	if toolID != "ffprobe" {
		return ports.Command{}, fmt.Errorf("测试 Resolver 只允许 ffprobe")
	}
	return ports.Command{
		Path: r.path,
		Args: append([]string(nil), args...),
		Dir:  workingDir,
	}, nil
}

func requireActualFFprobe(t *testing.T) string {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv(actualFFprobePathEnv))
	if configured == "" {
		t.Skipf("未设置 %s，跳过真实 ffprobe 门禁", actualFFprobePathEnv)
	}
	if !filepath.IsAbs(configured) {
		t.Fatalf("%s 必须是绝对路径", actualFFprobePathEnv)
	}
	info, err := os.Stat(configured)
	if err != nil || info.IsDir() {
		t.Fatalf("%s 未指向可读普通文件", actualFFprobePathEnv)
	}
	base := filepath.Base(configured)
	if !strings.EqualFold(base, "ffprobe") && !strings.EqualFold(base, "ffprobe.exe") {
		t.Fatalf("%s 只接受名为 ffprobe/ffprobe.exe 的显式工具", actualFFprobePathEnv)
	}
	return configured
}

type actualFFprobeHarness struct {
	service *toolrunner.Service
	store   *jobs.Store
}

func actualFFprobeService(t *testing.T, path string, seed int) actualFFprobeHarness {
	t.Helper()
	jobStore, _, _ := newJobStore(t, seed)
	service, err := toolrunner.New(jobStore, process.Controller{WaitDelay: 30 * time.Second}, explicitFFprobeResolver{path: path})
	if err != nil {
		t.Fatal(err)
	}
	return actualFFprobeHarness{service: service, store: jobStore}
}

func executeActualFFprobeExpectFailure(t *testing.T, harness actualFFprobeHarness, request toolrunner.Request) time.Duration {
	t.Helper()
	job, err := harness.service.Create(context.Background(), request, "owner")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	execErr := harness.service.Execute(context.Background(), job.ID)
	elapsed := time.Since(started)
	if execErr == nil {
		t.Fatal("真实 ffprobe 输入应失败，但 Execute 报告成功")
	}
	assertToolJobFailed(t, harness.store, job.ID)
	return elapsed
}

func TestActualFFprobeBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实 ffprobe 门禁")
	}
	ffprobePath := requireActualFFprobe(t)

	t.Run("版本输出基线", func(t *testing.T) {
		harness := actualFFprobeService(t, ffprobePath, 19)
		job, err := harness.service.Create(context.Background(), toolrunner.Request{
			ToolID: "ffprobe", Args: []string{"-version"}, TimeoutSeconds: 60, MaxOutputBytes: 64 << 10,
		}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if err := harness.service.Execute(context.Background(), job.ID); err != nil {
			t.Fatalf("真实 ffprobe 版本基线失败: %v", err)
		}
		completed, err := harness.store.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != jobs.StatusCompleted {
			t.Fatalf("版本基线 Job 终态 = %s，期望 completed", completed.Status)
		}
		var result toolrunner.Result
		if err := json.Unmarshal(completed.ResultJSON, &result); err != nil {
			t.Fatal(err)
		}
		if result.StdoutBytes <= 128 {
			t.Fatalf("ffprobe -version stdout = %d bytes，不能证明后续 128-byte 上限被实际触及", result.StdoutBytes)
		}
	})

	t.Run("输出溢出", func(t *testing.T) {
		service := actualFFprobeService(t, ffprobePath, 20)
		elapsed := executeActualFFprobeExpectFailure(t, service, toolrunner.Request{
			ToolID: "ffprobe", Args: []string{"-version"}, TimeoutSeconds: 60, MaxOutputBytes: 128,
		})
		if elapsed > 20*time.Second {
			t.Fatalf("真实 ffprobe 输出溢出未快速收敛: %v", elapsed)
		}
	})

	t.Run("执行超时", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		release := make(chan struct{})
		defer close(release)
		accepted := make(chan struct{}, 1)
		acceptErr := make(chan error, 1)
		go func() {
			connection, acceptError := listener.Accept()
			if acceptError != nil {
				acceptErr <- acceptError
				return
			}
			defer connection.Close()
			accepted <- struct{}{}
			<-release
		}()

		const timeoutSeconds = 3
		service := actualFFprobeService(t, ffprobePath, 21)
		elapsed := executeActualFFprobeExpectFailure(t, service, toolrunner.Request{
			ToolID:         "ffprobe",
			Args:           []string{"-v", "error", "-show_format", "-of", "json", "tcp://" + listener.Addr().String()},
			TimeoutSeconds: timeoutSeconds,
			MaxOutputBytes: 64 << 10,
		})
		if elapsed < timeoutSeconds*time.Second || elapsed > 20*time.Second {
			t.Fatalf("真实 ffprobe 超时耗时不在预期窗口: %v", elapsed)
		}
		select {
		case <-accepted:
		case acceptError := <-acceptErr:
			t.Fatalf("ffprobe 未建立 loopback 输入连接: %v", acceptError)
		default:
			t.Fatal("ffprobe 未建立 loopback 输入连接，超时测试前提不成立")
		}
	})

	t.Run("截断容器", func(t *testing.T) {
		// 有效 ftyp 后声明一个远大于实际文件的 extended-size mdat，构造纯合成的截断 MP4；
		// 不读取真实媒体，也不把该样本扩大描述为恶意容器语料库。
		container := []byte{
			0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0,
			'i', 's', 'o', 'm', 'i', 's', 'o', '2',
			0, 0, 0, 1, 'm', 'd', 'a', 't', 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		}
		input := filepath.Join(t.TempDir(), "truncated.mp4")
		if err := os.WriteFile(input, container, 0o600); err != nil {
			t.Fatal(err)
		}
		service := actualFFprobeService(t, ffprobePath, 22)
		elapsed := executeActualFFprobeExpectFailure(t, service, toolrunner.Request{
			ToolID:         "ffprobe",
			Args:           []string{"-v", "error", "-show_streams", "-show_format", "-of", "json", input},
			TimeoutSeconds: 10,
			MaxOutputBytes: 64 << 10,
		})
		if elapsed > 10*time.Second {
			t.Fatalf("真实 ffprobe 截断容器未在请求上限内失败: %v", elapsed)
		}
	})
}
