package rules_test

import (
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

func packageWithExtensions(extensions string) []byte {
	const template = `{
  "rule_set_id": "rset_018f47d2-5c16-7a44-a8a0-000000000001",
  "version": "0.1.0",
  "schema_version": 1,
  "normalization_algorithm_version": "gallery-canonical-json-v1",
  "compiler_requirement": "gallery-rule-compiler-v1",
  "cel_profile_version": "gallery-cel-v1",
  "parameter_schema": {"type": "object", "additionalProperties": false},
  "provider_namespaces": [],
  "primitives": [
    {"id": "work", "kind": "path_match", "config": {"scope": "work_directory", "glob": "*", "title": "directory_name", "stable_key": "relative_path", "metadata_file": "metadata.json"}},
    {"id": "media", "kind": "media_classify", "config": {"glob": "*.bin", "kind": "image", "mime": "application/octet-stream"}}
  ],
  "cel_expressions": [],
  "tests": [{"id": "one"}],
  "extensions": EXTENSIONS
}`
	return []byte(strings.Replace(template, "EXTENSIONS", extensions, 1))
}

func mustCompile(t *testing.T, extensions string) rules.CompiledPackage {
	t.Helper()
	compiled, err := rules.CompilePackage(packageWithExtensions(extensions))
	if err != nil {
		t.Fatalf("extensions %s 编译失败: %v", extensions, err)
	}
	return compiled
}

func TestExtensionIdentityClassification(t *testing.T) {
	baseline := mustCompile(t, `{}`)

	// 遗留未分类 extension：optional+nonsemantic，semantic_hash 不变，package_hash 改变。
	legacy := mustCompile(t, `{"example.legacy": {"preserved": true}}`)
	if legacy.SemanticHash != baseline.SemanticHash {
		t.Fatal("遗留 extension 改变了 semantic_hash")
	}
	if legacy.PackageHash == baseline.PackageHash {
		t.Fatal("遗留 extension 未改变 package_hash")
	}

	// optional+nonsemantic 且未知 namespace：容忍，semantic_hash 不变，package_hash 改变。
	optNon := mustCompile(t, `{"vendor.custom": {"required": false, "semantic": false, "payload": {"x": 1}}}`)
	if optNon.SemanticHash != baseline.SemanticHash {
		t.Fatal("optional+nonsemantic extension 改变了 semantic_hash")
	}
	if optNon.PackageHash == baseline.PackageHash {
		t.Fatal("optional+nonsemantic extension 未改变 package_hash")
	}

	// required+nonsemantic 且受支持：可编译，semantic_hash 不变。
	reqNon := mustCompile(t, `{"gallery.identity": {"required": true, "semantic": false, "version": "1"}}`)
	if reqNon.SemanticHash != baseline.SemanticHash {
		t.Fatal("required+nonsemantic extension 改变了 semantic_hash")
	}

	// optional+semantic 且受支持：semantic_hash 改变。
	optSem := mustCompile(t, `{"gallery.identity": {"required": false, "semantic": true, "version": "1", "payload": {"x": 1}}}`)
	if optSem.SemanticHash == baseline.SemanticHash {
		t.Fatal("optional+semantic extension 未改变 semantic_hash")
	}

	// required+semantic 且受支持：semantic_hash 改变。
	reqSem := mustCompile(t, `{"gallery.identity": {"required": true, "semantic": true, "version": "1", "payload": {"x": 1}}}`)
	if reqSem.SemanticHash == baseline.SemanticHash {
		t.Fatal("required+semantic extension 未改变 semantic_hash")
	}
}

func TestExtensionSemanticPayloadNormalization(t *testing.T) {
	compact := mustCompile(t, `{"gallery.identity": {"required": false, "semantic": true, "version": "1", "payload": {"a": 1, "b": 2}}}`)
	// 相同 semantic payload，仅键顺序与空白不同：规范化后 semantic_hash 必须一致。
	reordered := mustCompile(t, `{"gallery.identity": {"semantic": true, "required": false, "version": "1", "payload": {"b": 2, "a": 1}}}`)
	if compact.SemanticHash != reordered.SemanticHash {
		t.Fatal("相同 semantic payload 规范化后 semantic_hash 不一致")
	}
	// 不同 payload：semantic_hash 必须不同。
	different := mustCompile(t, `{"gallery.identity": {"required": false, "semantic": true, "version": "1", "payload": {"a": 1, "b": 3}}}`)
	if compact.SemanticHash == different.SemanticHash {
		t.Fatal("不同 semantic payload 得到相同 semantic_hash")
	}
}

func TestExtensionUnsupportedRejected(t *testing.T) {
	cases := map[string]string{
		"required+semantic 未知 namespace":    `{"vendor.unknown": {"required": true, "semantic": true, "version": "1"}}`,
		"optional+semantic 未知 namespace":    `{"vendor.unknown": {"required": false, "semantic": true, "version": "1"}}`,
		"required+nonsemantic 未知 namespace": `{"vendor.unknown": {"required": true, "semantic": false, "version": "1"}}`,
		"semantic 缺少 version":               `{"gallery.identity": {"required": false, "semantic": true}}`,
		"semantic 版本不受支持":                   `{"gallery.identity": {"required": false, "semantic": true, "version": "99"}}`,
		"分类结构含未知字段":                         `{"gallery.identity": {"semantic": true, "version": "1", "bogus": 1}}`,
	}
	for name, extensions := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := rules.CompilePackage(packageWithExtensions(extensions)); err == nil {
				t.Fatalf("不支持的 extension 未被拒绝: %s", extensions)
			}
		})
	}
}
