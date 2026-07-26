package rules_test

import (
	"testing"

	"github.com/RecRivenVI/gallery/internal/rules"
)

// TestPathCaptureFillsOnlyEmptyFields 锁定路径取值的核心语义：它是**回退**，不是覆盖。
//
// 真实规则把 `$path.author` 放在取值链末位——metadata 有作者时用 metadata，没有才用目录名。
// 「只填空」使这条优先级与原语的书写顺序无关，避免「调换两条原语的位置，作者名就悄悄变了」。
func TestPathCaptureFillsOnlyEmptyFields(t *testing.T) {
	const primitives = `,
    {"id":"creator","kind":"fallback","config":{"target":"creator","pointers":["/user/name"]}},
    {"id":"path","kind":"path_capture","config":{
      "pattern":"^(?P<author>[^/]+)/(?P<work>[^/]+)$",
      "targets":{"creator":"author"}}}`

	// metadata 没有作者：路径取值补上作者目录名。
	fromPath := evaluateWork(t, primitives, "作者目录/作品目录", map[string]any{})
	if fromPath.Work.Creator != "作者目录" {
		t.Fatalf("metadata 缺作者时未由路径补上: %q", fromPath.Work.Creator)
	}

	// metadata 有作者：路径取值不得覆盖它。
	fromMetadata := evaluateWork(t, primitives, "作者目录/作品目录",
		map[string]any{"user": map[string]any{"name": "元数据作者"}})
	if fromMetadata.Work.Creator != "元数据作者" {
		t.Fatalf("路径取值覆盖了 metadata 的作者: %q", fromMetadata.Work.Creator)
	}
}

// TestPathCaptureIsOrderIndependent 证明把 path_capture 写在 metadata 取值之前也得到同样结果。
func TestPathCaptureIsOrderIndependent(t *testing.T) {
	const capture = `{"id":"path","kind":"path_capture","config":{
      "pattern":"^(?P<author>[^/]+)/(?P<work>[^/]+)$","targets":{"creator":"author"}}}`
	const chain = `{"id":"creator","kind":"fallback","config":{"target":"creator","pointers":["/user/name"]}}`
	metadata := map[string]any{"user": map[string]any{"name": "元数据作者"}}

	captureFirst := evaluateWork(t, ",\n    "+capture+",\n    "+chain, "作者目录/作品目录", metadata)
	chainFirst := evaluateWork(t, ",\n    "+chain+",\n    "+capture, "作者目录/作品目录", metadata)
	if captureFirst.Work.Creator != chainFirst.Work.Creator {
		t.Fatalf("原语顺序改变了结果: %q vs %q", captureFirst.Work.Creator, chainFirst.Work.Creator)
	}
	if captureFirst.Work.Creator != "元数据作者" {
		t.Fatalf("metadata 未取胜: %q", captureFirst.Work.Creator)
	}
}

// TestPathCaptureRejectsInvalidConfiguration 锁定编译期校验：非法正则、引用不存在的捕获组、
// 不可赋值的目标字段与 date 都必须在规则发布时被拒绝，而不是在扫描时逐作品失败。
func TestPathCaptureRejectsInvalidConfiguration(t *testing.T) {
	for _, item := range []struct{ name, primitives string }{
		{"缺少 pattern", `,{"id":"p","kind":"path_capture","config":{"targets":{"creator":"a"}}}`},
		{"缺少 targets", `,{"id":"p","kind":"path_capture","config":{"pattern":"^(?P<a>[^/]+)"}}`},
		{"非法正则", `,{"id":"p","kind":"path_capture","config":{"pattern":"^(?P<a>[","targets":{"creator":"a"}}}`},
		{"没有命名捕获组", `,{"id":"p","kind":"path_capture","config":{"pattern":"^[^/]+","targets":{"creator":"a"}}}`},
		{"引用不存在的组", `,{"id":"p","kind":"path_capture","config":{"pattern":"^(?P<a>[^/]+)","targets":{"creator":"b"}}}`},
		{"不可赋值的目标", `,{"id":"p","kind":"path_capture","config":{"pattern":"^(?P<a>[^/]+)","targets":{"nonexistent":"a"}}}`},
		{"不能赋值 date", `,{"id":"p","kind":"path_capture","config":{"pattern":"^(?P<a>[^/]+)","targets":{"date":"a"}}}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := rules.CompilePackage([]byte(datePackage(item.primitives))); err == nil {
				t.Fatal("非法 path_capture 配置未在编译期被拒绝")
			}
		})
	}
}

// TestPathCaptureRecordsMissWithoutFailing 证明路径不匹配时留下可解释的 issue 而不是中断扫描。
func TestPathCaptureRecordsMissWithoutFailing(t *testing.T) {
	const primitives = `,
    {"id":"path","kind":"path_capture","config":{
      "pattern":"^(?P<author>[^/]+)/(?P<work>[^/]+)$","targets":{"creator":"author"}}}`
	result := evaluateWork(t, primitives, "只有一层", map[string]any{})
	if result.Work.Creator != "" {
		t.Fatalf("不匹配却赋了值: %q", result.Work.Creator)
	}
	found := false
	for _, issue := range result.Issues {
		if issue.Code == "RULE_PATH_CAPTURE_MISSING" {
			found = true
		}
	}
	if !found {
		t.Fatalf("路径不匹配时缺少 issue: %+v", result.Issues)
	}
}

// TestPathCaptureFeedsMultipleTargetsFromOneGroup 锁定「一个捕获组喂多个字段」。
//
// 映射方向若写成「组 → 字段」，同时把作者段用作创作者和标题的平台（真实配置里确实存在，该平台的
// 「作者」其实是书名）会让两个目标共用同一个键而互相覆盖，静默丢掉其中一个。转换器的 golden
// 正是暴露这个问题的地方。
func TestPathCaptureFeedsMultipleTargetsFromOneGroup(t *testing.T) {
	const primitives = `,
    {"id":"path","kind":"path_capture","config":{
      "pattern":"^(?P<author>[^/]+)/(?P<work>[^/]+)$",
      "targets":{"creator":"author","title":"author"}}}`
	// 标题已由 path_match 的 directory_name 填成作品目录名，因此断言 creator 取到作者段，
	// 而 title 保持既有值——「只填空」不覆盖已有值。
	result := evaluateWork(t, primitives, "作者目录/作品目录", map[string]any{})
	if result.Work.Creator != "作者目录" {
		t.Fatalf("creator 未从作者段取到: %q", result.Work.Creator)
	}
	if result.Work.Title != "作品目录" {
		t.Fatalf("title 被路径取值覆盖: %q", result.Work.Title)
	}
}
