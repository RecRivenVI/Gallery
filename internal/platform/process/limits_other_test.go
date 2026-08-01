//go:build !windows

package process_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/ports"
)

func TestUnsupportedHardLimitsFailBeforeProcessStart(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	controller := process.Controller{}
	if controller.SupportsLimits() {
		t.Fatal("当前 Unix 适配器错误报告支持进程树硬限制")
	}
	_, err = controller.Start(context.Background(), ports.Command{
		Path:   executable,
		Limits: ports.ProcessLimits{MemoryBytes: 64 << 20, CPUTime: time.Second},
	})
	if err == nil {
		t.Fatal("不支持的进程树硬限制没有在启动前 fail-closed")
	}
}
