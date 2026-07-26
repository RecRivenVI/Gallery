package media

import "strings"

// Presentation 决定一份媒体正文以什么 `Content-Type` 与 `Content-Disposition` 送出。
//
// 规则包可以为任意文件声明任意 MIME（`media_classify.config.mime`），而正文与
// `/api/v1` 同源。若直接把规则声明的 MIME 原样内联返回，一个能写入 Source 的人（或一个
// 被投毒的规则包）就能让浏览器在 Gallery 自己的源上执行 HTML/SVG 脚本，构成存储型 XSS。
// 因此对外呈现不信任规则声明，只信任本文件的白名单。
type Presentation struct {
	// ContentType 是实际送出的 `Content-Type`，不一定等于规则声明的 MIME。
	ContentType string
	// Attachment 为真时必须发送 `Content-Disposition: attachment`，让浏览器下载而不是
	// 在同源上下文中渲染。
	Attachment bool
}

// fallbackContentType 是所有不被信任为可内联的类型的统一呈现方式：不可嗅探的二进制流。
const fallbackContentType = "application/octet-stream"

// inlineImageTypes 与 inlineVideoTypes 是允许在同源上下文内联渲染的完整白名单。
//
// 入选条件是「浏览器按图片/视频解码器处理，不建立脚本执行上下文」。因此：
//   - `image/svg+xml` 被刻意排除：SVG 是可以携带 <script> 与外部引用的 XML 文档；
//   - 任何 `text/*`、`application/xml`、`text/html` 与未知类型都不在白名单内；
//   - 字幕等旁挂文本同样不内联，由客户端按需下载后自行解析。
//
// 白名单覆盖的是「Gallery 承诺可预览的类型」，与规则能识别的类型范围是两件事：不在白名单
// 内不代表文件损坏或不可用，只代表它以附件形式交付。
var inlineImageTypes = map[string]struct{}{
	"image/jpeg":               {},
	"image/png":                {},
	"image/gif":                {},
	"image/webp":               {},
	"image/avif":               {},
	"image/bmp":                {},
	"image/tiff":               {},
	"image/x-icon":             {},
	"image/vnd.microsoft.icon": {},
	"image/heic":               {},
	"image/heif":               {},
	"image/jxl":                {},
}

var inlineVideoTypes = map[string]struct{}{
	"video/mp4":        {},
	"video/webm":       {},
	"video/ogg":        {},
	"video/quicktime":  {},
	"video/x-matroska": {},
	"video/x-msvideo":  {},
	"audio/mpeg":       {},
	"audio/mp4":        {},
	"audio/ogg":        {},
	"audio/wav":        {},
	"audio/flac":       {},
	"audio/webm":       {},
}

// ResolvePresentation 把规则声明的 MIME 与客户端的下载意图收敛为实际呈现方式。
//
// download 为真时一律附件交付，不论类型是否可内联；download 为假时只有白名单内的类型
// 以原 MIME 内联，其余一律降级为 application/octet-stream 附件。降级只影响呈现，不影响
// 可用性：客户端仍然拿到完整正文。
func ResolvePresentation(declaredMIME string, download bool) Presentation {
	normalized := normalizeMIME(declaredMIME)
	if download {
		return Presentation{ContentType: fallbackContentType, Attachment: true}
	}
	if _, ok := inlineImageTypes[normalized]; ok {
		return Presentation{ContentType: normalized}
	}
	if _, ok := inlineVideoTypes[normalized]; ok {
		return Presentation{ContentType: normalized}
	}
	return Presentation{ContentType: fallbackContentType, Attachment: true}
}

// InlineAllowed 报告某个 MIME 是否在内联白名单内。它只表达呈现策略，不表达该类型是否
// 受支持，也不供规则或扫描端用于判定媒体种类。
func InlineAllowed(declaredMIME string) bool {
	normalized := normalizeMIME(declaredMIME)
	_, image := inlineImageTypes[normalized]
	_, video := inlineVideoTypes[normalized]
	return image || video
}

// normalizeMIME 去掉参数段（`; charset=...`）与两侧空白并折叠大小写。带参数的声明按其
// 基础类型判定，避免用参数绕过白名单；无法解析的输入落到空串，随后走降级路径。
func normalizeMIME(value string) string {
	base, _, _ := strings.Cut(value, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
