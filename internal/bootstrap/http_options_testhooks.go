//go:build !gallery_e2e_testhooks

package bootstrap

import "github.com/RecRivenVI/gallery/internal/transport/httpapi"

// applyHTTPOptionsTestHooks 在正式构建中是显式空操作。隔离浏览器运行器使用单独 build tag
// 收窄媒体读取闸门；正式 galleryd 不读取任何测试环境变量。
func applyHTTPOptionsTestHooks(_ *httpapi.Options) error { return nil }
