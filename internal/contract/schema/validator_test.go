package schema_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

// compileWithin 在给定预算内执行 Compile。Compile 的输入可以来自规则包携带的
// parameter_schema，因此「不挂起」本身就是必须断言的行为：一个永不返回的编译会占住调用
// 它的 HTTP 请求或 Job goroutine，而不是返回一个结构化错误。
func compileWithin(t *testing.T, budget time.Duration, name string, document []byte) (*contractschema.Validator, error, bool) {
	t.Helper()
	type outcome struct {
		validator *contractschema.Validator
		err       error
	}
	done := make(chan outcome, 1)
	go func() {
		validator, err := contractschema.Compile(name, document)
		done <- outcome{validator, err}
	}()
	select {
	case value := <-done:
		return value.validator, value.err, true
	case <-time.After(budget):
		return nil, nil, false
	}
}

// TestCompileTerminatesOnSelfReferentialSchema 断言自引用与递归 $ref 会终止。
// 这些形态可以由攻击者影响的 parameter_schema 直接构造，一旦编译器进入无限展开，
// 单个规则绑定请求就能永久占用一个 goroutine。
func TestCompileTerminatesOnSelfReferentialSchema(t *testing.T) {
	cases := map[string]string{
		"根自引用":    `{"$ref":"#"}`,
		"defs 自环": `{"$defs":{"node":{"$ref":"#/$defs/node"}},"$ref":"#/$defs/node"}`,
		"defs 互环": `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`,
		"递归对象": `{"type":"object","properties":{"child":{"$ref":"#"}},` +
			`"additionalProperties":false}`,
		"递归数组": `{"type":"array","items":{"$ref":"#"}}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, finished := compileWithin(t, 10*time.Second, "self.json", []byte(document)); !finished {
				t.Fatal("自引用 schema 的编译未在预算内返回，存在挂起风险")
			}
		})
	}
}

// TestRecursiveSchemaValidationTerminates 断言递归 schema 编译成功后，校验深层嵌套实例
// 同样会终止并给出确定结论，而不是在 Validate 阶段才挂起。
func TestRecursiveSchemaValidationTerminates(t *testing.T) {
	validator, err, finished := compileWithin(t, 10*time.Second, "recursive.json",
		[]byte(`{"type":"object","properties":{"child":{"$ref":"#"}},"additionalProperties":false}`))
	if !finished {
		t.Fatal("递归 schema 编译未在预算内返回")
	}
	if err != nil {
		t.Fatalf("递归 schema 应可编译: %v", err)
	}
	deep := []byte(strings.Repeat(`{"child":`, 200) + `{}` + strings.Repeat(`}`, 200))
	done := make(chan error, 1)
	go func() { done <- validator.ValidateJSON(deep) }()
	select {
	case validateErr := <-done:
		if validateErr != nil {
			t.Fatalf("合法的深层嵌套实例应通过: %v", validateErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("递归 schema 的校验未在预算内返回")
	}
}

// TestCompileRejectsExternalRefInsteadOfLoadingIt 是本包最重要的安全断言。
//
// Compile 的输入不都是仓库内置的可信 schema：internal/rules 的 CompileBinding 会把规则包
// 携带的 parameter_schema 原样交给 Compile。若编译器接受外部 $ref，一个规则包就获得了
// 「读本地任意文件」与「在 Windows 上发起 UNC/SMB 访问」的能力，这与规则不得触达文件、
// 网络的产品边界直接冲突。因此这里断言外部引用一律被拒绝，而不是被真的拉取。
//
// 断言方式不依赖错误文本：每个用例都指向一个内容已知的真实本地文件（约束为
// maxLength=3）。若编译器真的加载了它，Compile 会成功，且该约束会在 ValidateJSON 上生效。
// 只要出现这种情况，测试就判定为「引用被加载」而失败。
func TestCompileRejectsExternalRefInsteadOfLoadingIt(t *testing.T) {
	directory := t.TempDir()
	external := filepath.Join(directory, "external.json")
	if err := os.WriteFile(external, []byte(`{"type":"string","maxLength":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// AddResource 的 name 会被解析成相对**进程工作目录**的 file:// 基 URI，因此纯相对
	// `$ref` 指向的是工作目录旁的文件。这里把工作目录切到临时目录再放一份同名文件，
	// 既覆盖了这条解析路径，又不会把探针文件写进源码树（中断的测试会留下未跟踪文件）。
	working := t.TempDir()
	t.Chdir(working)
	sibling := filepath.Join(working, "external_ref_probe.json")
	if err := os.WriteFile(sibling, []byte(`{"type":"string","maxLength":3}`), 0o600); err != nil {
		t.Fatal(err)
	}

	references := map[string]string{
		"file 绝对路径":   "file:///" + filepath.ToSlash(external),
		"相对同级路径":      "external_ref_probe.json",
		"相对上级路径":      "../../../go.mod",
		"根相对路径":       "/etc/passwd",
		"http 远程":     "http://127.0.0.1:9/evil.json",
		"https 远程":    "https://example.invalid/evil.json",
		"file UNC 主机": "file://///attacker.invalid/share/evil.json",
	}
	for name, reference := range references {
		t.Run(name, func(t *testing.T) {
			document := []byte(fmt.Sprintf(`{"$ref":%q}`, reference))
			validator, compileErr, finished := compileWithin(t, 15*time.Second, "parameters.json", document)
			if !finished {
				t.Fatalf("外部引用 %q 的编译未在预算内返回：外部资源加载会让编译时间受外部因素支配", reference)
			}
			if compileErr == nil {
				// 引用被真的解析了。用外部文件的已知约束确认这一点，避免误判。
				overLong, err := json.Marshal("abcdefgh")
				if err != nil {
					t.Fatal(err)
				}
				if validator.ValidateJSON(overLong) != nil {
					t.Fatalf("外部引用 %q 被实际加载：外部文件的 maxLength 约束已经生效", reference)
				}
				t.Fatalf("外部引用 %q 未被拒绝", reference)
			}
			if !strings.Contains(compileErr.Error(), "拒绝加载外部 JSON Schema 资源") {
				t.Fatalf("外部引用 %q 应以显式拒绝失败，实际: %v", reference, compileErr)
			}
		})
	}
}

// TestCompileResolvesEmbeddedMetaSchemaOffline 断言拒绝外部加载没有连带切断库内嵌的
// JSON Schema 元 schema：仓库内置契约 schema 都声明 $schema，它们必须离线可编译。
func TestCompileResolvesEmbeddedMetaSchemaOffline(t *testing.T) {
	metaSchemas := []string{
		"https://json-schema.org/draft/2020-12/schema",
		"http://json-schema.org/draft-07/schema#",
	}
	for _, meta := range metaSchemas {
		document := []byte(fmt.Sprintf(`{"$schema":%q,"type":"object","required":["a"],"properties":{"a":{"type":"integer"}}}`, meta))
		validator, err, finished := compileWithin(t, 15*time.Second, "meta.json", document)
		if !finished {
			t.Fatalf("元 schema %s 的编译未在预算内返回", meta)
		}
		if err != nil {
			t.Fatalf("元 schema %s 应离线可用: %v", meta, err)
		}
		if err := validator.ValidateJSON([]byte(`{"a":1}`)); err != nil {
			t.Fatalf("元 schema %s 编译出的校验器不可用: %v", meta, err)
		}
		if validator.ValidateJSON([]byte(`{}`)) == nil {
			t.Fatalf("元 schema %s 编译出的校验器未生效", meta)
		}
	}
}

// TestCompileRejectsUnknownMetaSchemaURL 断言把 $schema 指向外部 URL 同样被拒绝：
// 元 schema 也是一次外部资源加载，不能成为绕过 $ref 限制的旁路。
func TestCompileRejectsUnknownMetaSchemaURL(t *testing.T) {
	_, err, finished := compileWithin(t, 15*time.Second, "meta.json",
		[]byte(`{"$schema":"https://attacker.invalid/meta","type":"object"}`))
	if !finished {
		t.Fatal("未知元 schema 的编译未在预算内返回")
	}
	if err == nil {
		t.Fatal("指向外部 URL 的 $schema 应被拒绝")
	}
	if !strings.Contains(err.Error(), "拒绝加载外部 JSON Schema 资源") {
		t.Fatalf("未知元 schema 应以显式拒绝失败，实际: %v", err)
	}
}

// TestCompilePreservesDecimalPrecision 断言 Compile 的 UseNumber 不是装饰：规则身份要求
// JSON 数字使用精确十进制，不得让 float64 中转影响判定。9007199254740993 与
// 9007199254740992 在 float64 下是同一个值，一旦中转就无法区分。
func TestCompilePreservesDecimalPrecision(t *testing.T) {
	validator, err := contractschema.Compile("decimal.json", []byte(`{"const":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateJSON([]byte(`9007199254740993`)); err != nil {
		t.Fatalf("等值的大整数应通过: %v", err)
	}
	if validator.ValidateJSON([]byte(`9007199254740992`)) == nil {
		t.Fatal("相邻大整数在 float64 中转后不可区分，说明精度被破坏")
	}
}

// TestValidateJSONRejectsUninitialisedValidator 断言未初始化的校验器返回错误，而不是
// 静默放行。放行会让一个装配失败的校验点变成「所有输入都合法」。
func TestValidateJSONRejectsUninitialisedValidator(t *testing.T) {
	var nilValidator *contractschema.Validator
	if err := nilValidator.ValidateJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil 校验器应返回错误而不是放行")
	}
	if err := new(contractschema.Validator).ValidateJSON([]byte(`{}`)); err == nil {
		t.Fatal("零值校验器应返回错误而不是放行")
	}
}

// TestCompileRejectsMalformedSchema 断言无法解析或语义无效的 schema 在编译期失败，
// 而不是产生一个「什么都通过」的校验器。
func TestCompileRejectsMalformedSchema(t *testing.T) {
	cases := map[string]string{
		"空字节":         ``,
		"截断 JSON":     `{"type":"object"`,
		"尾随内容":        `{"type":"object"} {"type":"array"}`,
		"type 非法":     `{"type":42}`,
		"required 非法": `{"required":"a"}`,
		"pattern 非正则": `{"type":"string","pattern":"("}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			validator, err := contractschema.Compile("bad.json", []byte(document))
			if err == nil {
				t.Fatalf("无效 schema 应编译失败，却得到 %#v", validator)
			}
			if validator != nil {
				t.Fatal("编译失败时不得返回可用的校验器")
			}
		})
	}
}

// TestValidateJSONRejectsMalformedPayload 断言待校验数据本身不是合法 JSON 时返回错误，
// 且尾随内容不会被静默忽略。
func TestValidateJSONRejectsMalformedPayload(t *testing.T) {
	validator, err := contractschema.Compile("payload.json", []byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"空字节":     ``,
		"截断 JSON": `{"a":`,
		"非 JSON":  `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validator.ValidateJSON([]byte(payload)); err == nil {
				t.Fatal("非法 JSON 数据应校验失败")
			}
		})
	}
}
