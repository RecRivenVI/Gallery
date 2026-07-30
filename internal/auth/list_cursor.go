package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

const (
	securityListCursorVersion = 1
	securityListCursorMaxSize = 4096
	securityListDefaultLimit  = 50
	securityListMaxLimit      = 200
)

// ListPage 是 control.db 安全资源列表的有界 keyset 页面。安全审计保留独立的
// “服务端固定最新一段”契约，不使用本类型。
type ListPage[T any] struct {
	Items      []T
	NextCursor string
}

// securityListCursor 使用一个固定字段集合承载五种列表锚点。固定 JSON 形状使解码端
// 可以拒绝未知字段、重复/尾随 JSON 和非规范编码；kind/scope 的变化属于可恢复的
// cursor 作用域变化，而不是把旧锚点静默套到另一类资源或另一个 Principal。
type securityListCursor struct {
	Version    int    `json:"v"`
	Kind       string `json:"kind"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"createdAt"`
	ID         string `json:"id"`
	Username   string `json:"username"`
	Revoked    bool   `json:"revoked"`
	Capability string `json:"capability"`
	ScopeKind  string `json:"scopeKind"`
	ScopeID    string `json:"scopeId"`
}

func normalizeSecurityListLimit(limit int) int {
	if limit <= 0 || limit > securityListMaxLimit {
		return securityListDefaultLimit
	}
	return limit
}

func encodeSecurityListCursor(cursor securityListCursor) string {
	cursor.Version = securityListCursorVersion
	raw, err := json.Marshal(cursor)
	if err != nil {
		panic(err) // 固定字段结构不会产生 JSON 编码错误。
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeSecurityListCursor(token, kind, scope string) (securityListCursor, error) {
	if len(token) == 0 || len(token) > securityListCursorMaxSize {
		return securityListCursor{}, invalidSecurityListCursor(nil)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return securityListCursor{}, invalidSecurityListCursor(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor securityListCursor
	if err := decoder.Decode(&cursor); err != nil {
		return securityListCursor{}, invalidSecurityListCursor(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return securityListCursor{}, invalidSecurityListCursor(err)
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, raw) {
		return securityListCursor{}, invalidSecurityListCursor(err)
	}
	if cursor.Version != securityListCursorVersion || cursor.Kind != kind || cursor.Scope != scope {
		return securityListCursor{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	return cursor, nil
}

func invalidSecurityListCursor(err error) error {
	return fault.New(fault.CodeCursorInvalid, false, err)
}
