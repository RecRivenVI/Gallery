package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestLibrarySourceRuleVersionAndBindingArePersistent(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixedClock := clock.Fixed{Time: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)}
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, fixedClock, identity.NewGenerator(fixedClock))
	if err != nil {
		t.Fatal(err)
	}
	library, err := resources.CreateLibrary(context.Background(), "Walking Skeleton")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(context.Background(), library.ID, "Synthetic", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	packageJSON, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := resources.CreateRuleVersion(context.Background(), packageJSON)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := resources.CreateSourceRuleBinding(context.Background(), source.ID, version.SemanticHash, []byte("{}"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if binding.SourceID != source.ID || binding.IR.MediaGlob != "*.bin" {
		t.Fatalf("绑定未冻结正式 Rule IR: %+v", binding)
	}
	if _, err := resources.GetLibrary(context.Background(), library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := resources.GetSourceRuleBinding(context.Background(), binding.ID); err != nil {
		t.Fatal(err)
	}

	_, err = resources.CreateSource(context.Background(), library.ID, "overlap", filepath.Join(dirs.Data, "inside"))
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeSourcePathInvalid {
		t.Fatalf("不存在的 AppDirs 内路径错误 = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dirs.Data, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = resources.CreateSource(context.Background(), library.ID, "overlap", filepath.Join(dirs.Data, "inside"))
	if !errors.As(err, &structured) || structured.Code != fault.CodeAppDirsOverlap {
		t.Fatalf("Source/AppDirs 重叠未拒绝: %v", err)
	}
}
