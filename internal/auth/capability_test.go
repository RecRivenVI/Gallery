package auth_test

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/auth"
)

// TestDerivedAssetGenerationCapabilityIsIndependentOfMediaRead 证明 media.derive 是独立于
// media.read 的 capability：DerivedAsset 生成 handler 检查 media.derive，读取已生成正文的
// handler 检查 media.read（见 server.go 的 createDerivedAsset/derivedAssetContent）。这里
// 直接验证 HasCapability 语义——一个只拥有 media.read（没有 media.derive）的会话必须被
// 拒绝生成，但仍可以读取；这是 requireCapability 实际依赖的判定原语，不需要等到阶段 5
// 引入第二个账户/角色才能验证该边界。
func TestDerivedAssetGenerationCapabilityIsIndependentOfMediaRead(t *testing.T) {
	readOnlyMedia := auth.Session{PrincipalID: "test-principal", Capabilities: []string{"media.read"}}
	if auth.HasCapability(readOnlyMedia, "media.derive") {
		t.Fatalf("只拥有 media.read 的会话不应通过 media.derive 检查")
	}
	if !auth.HasCapability(readOnlyMedia, "media.read") {
		t.Fatalf("只读媒体会话应仍能通过 media.read 检查")
	}

	full := auth.Session{PrincipalID: "test-principal", Capabilities: append([]string(nil), auth.PersonalOwnerCapabilities...)}
	if !auth.HasCapability(full, "media.derive") || !auth.HasCapability(full, "media.read") {
		t.Fatalf("Personal owner 默认应同时拥有 media.derive 与 media.read: %+v", full.Capabilities)
	}
}

// TestPersonalOwnerCapabilitiesIncludesMediaDerive 证明 media.derive 已注册为默认 Personal
// owner/admin capability 集合的一员（阶段 5 之前只有单一 owner 角色，默认必须拥有）。
func TestPersonalOwnerCapabilitiesIncludesMediaDerive(t *testing.T) {
	found := false
	for _, capability := range auth.PersonalOwnerCapabilities {
		if capability == "media.derive" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("PersonalOwnerCapabilities 缺少 media.derive: %+v", auth.PersonalOwnerCapabilities)
	}
}
