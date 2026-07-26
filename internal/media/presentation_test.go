package media_test

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/media"
)

// TestResolvePresentationOnlyInlinesWhitelistedTypes 是 SEC-3 的核心回归：规则包声明的
// MIME 不能决定正文是否在同源上下文渲染。白名单之外的一切——尤其是可携带脚本的 SVG 与
// HTML——必须降级为不可嗅探的 application/octet-stream 附件。
func TestResolvePresentationOnlyInlinesWhitelistedTypes(t *testing.T) {
	for _, item := range []struct {
		name           string
		declared       string
		wantType       string
		wantAttachment bool
	}{
		{"JPEG 内联", "image/jpeg", "image/jpeg", false},
		{"PNG 内联", "image/png", "image/png", false},
		{"AVIF 内联", "image/avif", "image/avif", false},
		{"MP4 内联", "video/mp4", "video/mp4", false},
		{"带参数声明按基础类型判定", "image/png; charset=utf-8", "image/png", false},
		{"大小写与空白不影响判定", "  IMAGE/JPEG  ", "image/jpeg", false},
		{"SVG 永不内联", "image/svg+xml", "application/octet-stream", true},
		{"HTML 永不内联", "text/html", "application/octet-stream", true},
		{"XML 永不内联", "application/xml", "application/octet-stream", true},
		{"JavaScript 永不内联", "text/javascript", "application/octet-stream", true},
		{"未知类型降级", "application/x-gallery-unknown", "application/octet-stream", true},
		{"空声明降级", "", "application/octet-stream", true},
		{"参数伪装无法绕过白名单", "text/html; x=image/png", "application/octet-stream", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			got := media.ResolvePresentation(item.declared, false)
			if got.ContentType != item.wantType || got.Attachment != item.wantAttachment {
				t.Fatalf("呈现 = %+v want type=%s attachment=%v", got, item.wantType, item.wantAttachment)
			}
			if media.InlineAllowed(item.declared) != !item.wantAttachment {
				t.Fatalf("InlineAllowed 与呈现结论不一致: %s", item.declared)
			}
		})
	}
}

// TestResolvePresentationForcesAttachmentOnDownload 证明显式下载请求一律附件交付，即使
// 类型本身可内联；下载路径不得给出可在同源渲染的 Content-Type。
func TestResolvePresentationForcesAttachmentOnDownload(t *testing.T) {
	for _, declared := range []string{"image/jpeg", "video/mp4", "image/svg+xml", ""} {
		got := media.ResolvePresentation(declared, true)
		if got.ContentType != "application/octet-stream" || !got.Attachment {
			t.Fatalf("download 呈现 = %+v declared=%q", got, declared)
		}
	}
}
