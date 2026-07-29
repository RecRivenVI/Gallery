//go:build !gallery_e2e_testhooks

package httpapi

import "context"

// waitForMediaReadTestHook 在正式构建中是显式空操作；E2E 专用构建会在已取得闸门名额并
// 打开合成 Source 句柄后建立确定性阻塞窗口。
func waitForMediaReadTestHook(_ context.Context, _ string) error { return nil }
