//go:build windows

package process_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/ports"
)

func TestCPUTimeLimitTerminatesWholeProcessTree(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	command := helperCommand(t, roleSpawnCPU, heartbeat)
	command.Limits = ports.ProcessLimits{CPUTime: 750 * time.Millisecond}
	controller := process.Controller{WaitDelay: 5 * time.Second}
	if !controller.SupportsLimits() {
		t.Fatal("Windows ProcessController 错误报告不支持硬限制")
	}
	running, err := controller.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("启动 CPU 限制辅助进程失败: %v", err)
	}
	waitForHeartbeat(t, heartbeat)
	started := time.Now()
	if err := running.Wait(); err == nil {
		t.Fatal("达到进程树 CPU 时间上限后 Wait 报告成功")
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("CPU 时间上限未有界终止进程树: %v", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat)
}

func TestMemoryLimitAppliesToWholeProcessTree(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实进程用例")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	command := helperCommand(t, roleSpawnMemory, heartbeat)
	// 父子各自提交 192 MiB，单个分配都低于 384 MiB；二者合计已达到上限，再加两个
	// 进程自身的提交量必然越界，从而排除“只限制单进程”的伪实现。
	command.Limits = ports.ProcessLimits{MemoryBytes: 384 << 20}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	running, err := (process.Controller{WaitDelay: 5 * time.Second}).Start(ctx, command)
	if err != nil {
		t.Fatalf("启动内存限制辅助进程失败: %v", err)
	}
	waitForHeartbeat(t, heartbeat)
	started := time.Now()
	if err := running.Wait(); err == nil {
		t.Fatal("父子进程提交内存超过 Job 上限后 Wait 报告成功")
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("内存上限未有界拒绝进程树分配: %v", elapsed)
	}
	assertHeartbeatStopped(t, heartbeat)
}
