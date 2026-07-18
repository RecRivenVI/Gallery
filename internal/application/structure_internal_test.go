package application

import "testing"

// TestStructureFingerprintDeterministicAndEvidenceSensitive 直接校验结构证据指纹的稳定性与敏感度：
// 版本前缀、输入顺序无关、Blob 证据变化敏感、new→origin 映射变化敏感、跨 Source 不同。
func TestStructureFingerprintDeterministicAndEvidenceSensitive(t *testing.T) {
	base := structureCluster{
		kind: "split", sourceID: "src_a", representative: "wkA",
		originSourceKeys: []string{"wkA"}, originWorkIDs: []string{"wrk_x"},
		newSourceKeys: []string{"wkA1", "wkA2"},
		originDigests: map[string][]string{"wkA": {"sha-256:d1", "sha-256:d2", "sha-256:d3"}},
		newDigests:    map[string][]string{"wkA1": {"sha-256:d1", "sha-256:d2"}, "wkA2": {"sha-256:d3"}},
		mapping:       map[string][]string{"wkA1": {"wkA"}, "wkA2": {"wkA"}},
	}
	baseFP := structureFingerprint(base)
	if !isLegacyStructureFingerprint("split|wkA|wkA1\x00wkA2") {
		t.Fatal("旧格式指纹未被识别为 legacy")
	}
	if isLegacyStructureFingerprint(baseFP) || len(baseFP) < len(structureFingerprintPrefix)+64 {
		t.Fatalf("v2 指纹格式错误: %q", baseFP)
	}

	// 输入顺序无关：source_key、digest、mapping 顺序打乱后指纹不变。
	reordered := structureCluster{
		kind: "split", sourceID: "src_a", representative: "wkA",
		originSourceKeys: []string{"wkA"}, originWorkIDs: []string{"wrk_x"},
		newSourceKeys: []string{"wkA2", "wkA1"},
		originDigests: map[string][]string{"wkA": {"sha-256:d3", "sha-256:d1", "sha-256:d2"}},
		newDigests:    map[string][]string{"wkA2": {"sha-256:d3"}, "wkA1": {"sha-256:d2", "sha-256:d1"}},
		mapping:       map[string][]string{"wkA2": {"wkA"}, "wkA1": {"wkA"}},
	}
	if got := structureFingerprint(reordered); got != baseFP {
		t.Fatalf("顺序不同但语义相同的指纹不一致:\n base=%s\n got =%s", baseFP, got)
	}

	// source_key 相同但 Blob 归属变化（d2 由 wkA1 移到 wkA2）：指纹必须不同。
	blobMoved := base
	blobMoved.newDigests = map[string][]string{"wkA1": {"sha-256:d1"}, "wkA2": {"sha-256:d2", "sha-256:d3"}}
	if structureFingerprint(blobMoved) == baseFP {
		t.Fatal("Blob 归属变化未改变指纹")
	}

	// 算法版本变化也必须改变完整 Blob 证据身份。
	algorithmChanged := base
	algorithmChanged.originDigests = map[string][]string{"wkA": {"sha-512:d1", "sha-256:d2", "sha-256:d3"}}
	if structureFingerprint(algorithmChanged) == baseFP {
		t.Fatal("Blob 算法变化未改变指纹")
	}

	// source_key 与 Blob 集合相同但 new→origin 映射变化：指纹必须不同。
	mappingChanged := base
	mappingChanged.mapping = map[string][]string{"wkA1": {"wkA"}, "wkA2": {"wkA", "wkB"}}
	if structureFingerprint(mappingChanged) == baseFP {
		t.Fatal("映射变化未改变指纹")
	}

	// 规范化必须去重：重复的 source_key、digest 和映射边不应制造新的证据身份。
	withDuplicates := base
	withDuplicates.originSourceKeys = []string{"wkA", "wkA"}
	withDuplicates.originWorkIDs = []string{"wrk_x", "wrk_x"}
	withDuplicates.newSourceKeys = []string{"wkA2", "wkA1", "wkA1", "wkA2"}
	withDuplicates.originDigests = map[string][]string{"wkA": {"sha-256:d3", "sha-256:d1", "sha-256:d1", "sha-256:d2"}}
	withDuplicates.newDigests = map[string][]string{"wkA1": {"sha-256:d2", "sha-256:d1", "sha-256:d1"}, "wkA2": {"sha-256:d3", "sha-256:d3"}}
	withDuplicates.mapping = map[string][]string{"wkA1": {"wkA", "wkA"}, "wkA2": {"wkA", "wkA"}}
	if got := structureFingerprint(withDuplicates); got != baseFP {
		t.Fatalf("重复证据未被规范化去重: base=%s got=%s", baseFP, got)
	}

	// 候选 CanonicalWork 身份变化必须改变人工判断依据。
	workChanged := base
	workChanged.originWorkIDs = []string{"wrk_y"}
	if structureFingerprint(workChanged) == baseFP {
		t.Fatal("候选 CanonicalWork 身份变化未改变指纹")
	}

	// 不同 Source 相同结构：指纹必须不同。
	otherSource := base
	otherSource.sourceID = "src_b"
	if structureFingerprint(otherSource) == baseFP {
		t.Fatal("不同 Source 指纹相同")
	}

	// 当前簇的 legacy 复算应等于历史格式，用于兼容升级识别。
	if legacyStructureFingerprint(base) != "split|wkA|wkA1\x00wkA2" {
		t.Fatalf("legacy 复算错误: %q", legacyStructureFingerprint(base))
	}
}
