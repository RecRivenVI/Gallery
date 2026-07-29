//go:build gallery_e2e_testhooks

package httpapi

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

const (
	e2eBlockMediaRelativePathEnv = "GALLERY_E2E_BLOCK_MEDIA_RELATIVE_PATH"
	e2eMediaReadReadyFileEnv     = "GALLERY_E2E_MEDIA_READ_READY_FILE"
	e2eMediaReadReleaseFileEnv   = "GALLERY_E2E_MEDIA_READ_RELEASE_FILE"
)

var e2eMediaReadBlocked atomic.Bool

// waitForMediaReadTestHook 只存在于 gallery_e2e_testhooks 构建。首个匹配请求已经取得真实
// mediaGate 名额并打开只读 Source 句柄后写出 ready 标记，再等待测试进程写入 release 标记；
// 其它请求继续经过生产 acquireMediaRead，因而能确定性观察真实 MEDIA_READ_BUSY。
func waitForMediaReadTestHook(ctx context.Context, relativePath string) error {
	target := os.Getenv(e2eBlockMediaRelativePathEnv)
	if target == "" || relativePath != target || !e2eMediaReadBlocked.CompareAndSwap(false, true) {
		return nil
	}
	readyPath := os.Getenv(e2eMediaReadReadyFileEnv)
	releasePath := os.Getenv(e2eMediaReadReleaseFileEnv)
	if readyPath == "" || releasePath == "" {
		return fault.New(fault.CodeInternal, false, errors.New("媒体读取 E2E 阻塞标记路径未配置"))
	}
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
		return fault.New(fault.CodeInternal, false, err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(releasePath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fault.New(fault.CodeInternal, false, err)
		}
		select {
		case <-ctx.Done():
			return fault.New(fault.CodeProcessInterrupted, true, ctx.Err())
		case <-ticker.C:
		}
	}
}
