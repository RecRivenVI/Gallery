package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestRuleLifecycleDraftPublishParameterAndRollback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	packageJSON, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := resources.CreateRulePackage(ctx, "", "内置示例", "synthetic", "owner")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := resources.SaveRuleDraft(ctx, pkg.ID, packageJSON, "json", "", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if draft.ValidationStatus != application.RuleDraftValidated || draft.Revision != 1 {
		t.Fatalf("草稿状态错误: %+v", draft)
	}
	if _, err := resources.SaveRuleDraft(ctx, pkg.ID, packageJSON, "json", "", 1, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.SaveRuleDraft(ctx, pkg.ID, packageJSON, "json", "", 1, "owner"); err == nil {
		t.Fatal("重复 revision 保存未拒绝")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeRuleDraftConflict {
			t.Fatalf("草稿冲突错误码 = %v", err)
		}
	}
	draft, err = resources.GetRuleDraft(ctx, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	version, err := resources.PublishRuleDraft(ctx, pkg.ID, draft.Revision, "owner", "初始发布")
	if err != nil {
		t.Fatal(err)
	}
	if version.SemanticHash == "" || version.PackageID != pkg.ID || version.Status != application.RuleVersionPublished {
		t.Fatalf("发布版本错误: %+v", version)
	}
	changed := []byte(string(packageJSON))
	changed = []byte(strings.Replace(string(changed), `"version": "0.1.0"`, `"version": "0.2.0"`, 1))
	draft, err = resources.GetRuleDraft(ctx, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = resources.SaveRuleDraft(ctx, pkg.ID, changed, "json", version.SemanticHash, draft.Revision, "owner")
	if err != nil {
		t.Fatal(err)
	}
	secondVersion, err := resources.PublishRuleDraft(ctx, pkg.ID, draft.Revision, "owner", "测试第二版本")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err = resources.GetRulePackage(ctx, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := resources.RollbackRulePackage(ctx, pkg.ID, version.SemanticHash, pkg.Revision, "owner", "回滚测试", false)
	if err != nil || rolledBack.SemanticHash != version.SemanticHash {
		t.Fatalf("回滚失败: %+v %v", rolledBack, err)
	}
	parameter, err := resources.CreateRuleParameterSet(ctx, "默认参数", version.SemanticHash, []byte(`{}`), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if parameter.CurrentRevision != 1 || parameter.CurrentHash == "" {
		t.Fatalf("参数集错误: %+v", parameter)
	}
	parameter, err = resources.UpdateRuleParameterSet(ctx, parameter.ID, []byte(`{}`), 1, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if parameter.CurrentRevision != 2 {
		t.Fatalf("参数 revision 未递增: %+v", parameter)
	}
	firstCompile, err := resources.CompileRulePackage(ctx, packageJSON, []byte(`{}`))
	if err != nil || firstCompile.CacheHit {
		t.Fatalf("首次持久编译缓存错误: %+v %v", firstCompile, err)
	}
	secondCompile, err := resources.CompileRulePackage(ctx, packageJSON, []byte(`{}`))
	if err != nil || !secondCompile.CacheHit || secondCompile.RuleIRHash != firstCompile.RuleIRHash {
		t.Fatalf("持久编译缓存未命中: %+v %v", secondCompile, err)
	}
	if _, err := resources.DeprecateRuleVersion(ctx, version.SemanticHash, "owner", "测试旧版本"); err == nil {
		t.Fatal("当前版本被错误允许弃用")
	}
	if deprecated, err := resources.DeprecateRuleVersion(ctx, secondVersion.SemanticHash, "owner", "回滚后弃用"); err != nil || deprecated.Status != application.RuleVersionDeprecated {
		t.Fatalf("非当前版本弃用失败: %+v %v", deprecated, err)
	}
}

func TestRuleParameterBindingRefreshAndDeterministicSelection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	mediaRoot := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	packageJSON, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	packageJSON = []byte(strings.Replace(string(packageJSON), `"parameter_schema": {"type": "object", "additionalProperties": false}`, `"parameter_schema": {"type": "object", "properties": {"minimumSize": {"type": "integer", "minimum": 0}}, "additionalProperties": false}`, 1))
	pkg, err := resources.CreateRulePackage(ctx, "", "参数绑定规则", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := resources.SaveRuleDraft(ctx, pkg.ID, packageJSON, "json", "", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	version, err := resources.PublishRuleDraft(ctx, pkg.ID, draft.Revision, "owner", "参数绑定测试")
	if err != nil {
		t.Fatal(err)
	}
	parameter, err := resources.CreateRuleParameterSet(ctx, "共享参数", version.SemanticHash, []byte(`{"minimumSize":1}`), "owner")
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(ctx, "测试库")
	if err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "测试源", mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	condition, _ := json.Marshal(map[string]string{"sourceId": source.ID})
	first, err := resources.CreateSourceRuleBindingFromParameterSet(ctx, source.ID, parameter.ID, 10, []byte(`{"minimumSize":2}`), condition)
	if err != nil {
		t.Fatal(err)
	}
	if first.ParameterRevision != 1 || !strings.Contains(string(first.Parameters), `"minimumSize":2`) {
		t.Fatalf("Binding override 未冻结: %+v", first)
	}
	if _, err := resources.CreateSourceRuleBindingFromParameterSet(ctx, source.ID, parameter.ID, 10, nil, condition); err == nil {
		t.Fatal("同优先级 active Binding 未产生冲突")
	} else {
		var structured *fault.Error
		if !errors.As(err, &structured) || structured.Code != fault.CodeRuleBindingConflict {
			t.Fatalf("Binding 冲突错误码 = %v", err)
		}
	}
	otherCondition, _ := json.Marshal(map[string]string{"displayName": "其他源"})
	if _, err := resources.CreateSourceRuleBindingFromParameterSet(ctx, source.ID, parameter.ID, 5, nil, otherCondition); err != nil {
		t.Fatal(err)
	}
	bindings, err := resources.ListSourceRuleBindings(ctx, source.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("Binding 列表错误: %+v %v", bindings, err)
	}
	otherBindingID := bindings[0].ID
	if otherBindingID == first.ID {
		otherBindingID = bindings[1].ID
	}
	if _, err := resources.SetSourceRuleBindingStatus(ctx, otherBindingID, application.RuleBindingPaused); err != nil {
		t.Fatal(err)
	}
	effective, err := resources.BindingForSource(ctx, source.ID)
	if err != nil || effective.ID != first.ID {
		t.Fatalf("暂停后生效选择错误: %+v %v", effective, err)
	}
	impact, err := resources.ImpactRuleParameterSet(ctx, parameter.ID, []byte(`{"minimumSize":3}`))
	if err != nil || len(impact.AffectedSources) != 1 || impact.AffectedSources[0] != source.ID {
		t.Fatalf("共享参数 Impact 错误: %+v %v", impact, err)
	}
	updated, err := resources.UpdateRuleParameterSet(ctx, parameter.ID, []byte(`{"minimumSize":3}`), 1, "owner")
	if err != nil || updated.CurrentRevision != 2 {
		t.Fatalf("参数更新失败: %+v %v", updated, err)
	}
	effective, err = resources.BindingForSource(ctx, source.ID)
	if err != nil || effective.ParameterRevision != 2 || !strings.Contains(string(effective.Parameters), `"minimumSize":2`) {
		t.Fatalf("参数更新未保留 Binding override 快照: %+v %v", effective, err)
	}
}

func TestInvalidRuleDraftIsPreservedAcrossValidation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := resources.CreateRulePackage(ctx, "", "无效草稿", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	const invalid = `{"`
	draft, err := resources.SaveRuleDraft(ctx, pkg.ID, []byte(invalid), "json", "", 0, "owner")
	if err != nil || draft.ValidationStatus != application.RuleDraftInvalid || string(draft.Content) != invalid {
		t.Fatalf("无效草稿保存错误: %+v %v", draft, err)
	}
	validation, err := resources.ValidateRuleDraft(ctx, pkg.ID, draft.Revision, "owner")
	if err != nil || validation.Valid || string(validation.Draft.Content) != invalid || validation.Draft.ValidationStatus != application.RuleDraftInvalid {
		t.Fatalf("无效草稿校验后未保留: %+v %v", validation, err)
	}
}
