//go:build gallery_e2e_testhooks

package bootstrap

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/RecRivenVI/gallery/internal/transport/httpapi"
)

const (
	e2eMediaReadConcurrencyEnv = "GALLERY_E2E_MEDIA_READ_CONCURRENCY"
	e2eMediaReadGateWaitMSEnv  = "GALLERY_E2E_MEDIA_READ_GATE_WAIT_MS"
)

// applyHTTPOptionsTestHooks 只编译进隔离浏览器运行器使用的 galleryd。两个值必须同时存在并
// 为正数，避免半配置时悄悄退回正式默认值，制造没有真正触发背压的假阳性。
func applyHTTPOptionsTestHooks(options *httpapi.Options) error {
	concurrencyRaw, hasConcurrency := os.LookupEnv(e2eMediaReadConcurrencyEnv)
	waitRaw, hasWait := os.LookupEnv(e2eMediaReadGateWaitMSEnv)
	if !hasConcurrency && !hasWait {
		return nil
	}
	if !hasConcurrency || !hasWait {
		return fmt.Errorf("%s 与 %s 必须同时提供", e2eMediaReadConcurrencyEnv, e2eMediaReadGateWaitMSEnv)
	}
	concurrency, err := strconv.Atoi(concurrencyRaw)
	if err != nil || concurrency < 1 {
		return fmt.Errorf("%s 必须是正整数", e2eMediaReadConcurrencyEnv)
	}
	waitMS, err := strconv.Atoi(waitRaw)
	if err != nil || waitMS < 1 {
		return fmt.Errorf("%s 必须是正整数", e2eMediaReadGateWaitMSEnv)
	}
	options.MediaReadConcurrency = concurrency
	options.MediaReadGateWait = time.Duration(waitMS) * time.Millisecond
	return nil
}
