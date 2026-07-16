package domain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// IDKind 同时确定领域类型和公共 ID 前缀，防止不同实体 ID 被静默混用。
type IDKind string

const (
	IDLibrary           IDKind = "lib"
	IDSource            IDKind = "src"
	IDCanonicalWork     IDKind = "wrk"
	IDCanonicalCreator  IDKind = "ctr"
	IDCanonicalMedia    IDKind = "med"
	IDSourceRuleBinding IDKind = "srb"
	IDRuleSet           IDKind = "rset"
	IDJob               IDKind = "job"
	IDQueryPublication  IDKind = "qpub"
	IDSession           IDKind = "ses"
	IDCatalogRevision   IDKind = "crev"
	IDOverlayRevision   IDKind = "orev"
	IDWorkBinding       IDKind = "wbind"
	IDCreatorBinding    IDKind = "cbind"
	IDMediaBinding      IDKind = "mbind"
	IDBindingIssue      IDKind = "biss"
)

var validIDKinds = map[IDKind]struct{}{
	IDLibrary: {}, IDSource: {}, IDCanonicalWork: {}, IDCanonicalCreator: {},
	IDCanonicalMedia: {}, IDSourceRuleBinding: {}, IDRuleSet: {}, IDJob: {},
	IDQueryPublication: {}, IDSession: {},
	IDCatalogRevision: {}, IDOverlayRevision: {}, IDWorkBinding: {}, IDCreatorBinding: {}, IDMediaBinding: {}, IDBindingIssue: {},
}

// ID 是带类型前缀的 UUIDv7。零值不是合法领域 ID。
type ID struct {
	kind IDKind
	uuid [16]byte
}

func IDFromUUIDv7(kind IDKind, raw [16]byte) (ID, error) {
	if _, ok := validIDKinds[kind]; !ok {
		return ID{}, fmt.Errorf("未知 ID kind")
	}
	if raw[6]>>4 != 7 || raw[8]>>6 != 2 {
		return ID{}, fmt.Errorf("ID 不是 RFC 9562 UUIDv7")
	}
	return ID{kind: kind, uuid: raw}, nil
}

func ParseID(kind IDKind, value string) (ID, error) {
	prefix := string(kind) + "_"
	if !strings.HasPrefix(value, prefix) {
		return ID{}, fmt.Errorf("ID 前缀与类型不匹配")
	}
	uuidText := strings.TrimPrefix(value, prefix)
	if len(uuidText) != 36 || uuidText[8] != '-' || uuidText[13] != '-' || uuidText[18] != '-' || uuidText[23] != '-' {
		return ID{}, fmt.Errorf("ID UUID 格式无效")
	}
	hexText := strings.ReplaceAll(uuidText, "-", "")
	var raw [16]byte
	if _, err := hex.Decode(raw[:], []byte(hexText)); err != nil {
		return ID{}, fmt.Errorf("ID UUID 编码无效: %w", err)
	}
	return IDFromUUIDv7(kind, raw)
}

func (id ID) Kind() IDKind { return id.kind }

func (id ID) IsZero() bool { return id.kind == "" }

func (id ID) String() string {
	if id.IsZero() {
		return ""
	}
	b := id.uuid
	return fmt.Sprintf("%s_%08x-%04x-%04x-%04x-%012x", id.kind, b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (id ID) MarshalText() ([]byte, error) {
	if id.IsZero() {
		return nil, fmt.Errorf("零值不是合法领域 ID")
	}
	return []byte(id.String()), nil
}

func (id *ID) UnmarshalText(text []byte) error {
	if id == nil || id.kind == "" {
		return fmt.Errorf("反序列化 ID 前必须指定 kind")
	}
	parsed, err := ParseID(id.kind, string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	if id.IsZero() {
		return nil, fmt.Errorf("零值不是合法领域 ID")
	}
	return json.Marshal(id.String())
}
