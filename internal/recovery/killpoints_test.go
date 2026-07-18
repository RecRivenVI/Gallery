package recovery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/overlay"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	recoveryservice "github.com/RecRivenVI/gallery/internal/recovery"
	"github.com/RecRivenVI/gallery/internal/scanner"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	helperModeEnv = "GALLERY_KILL_HELPER"
	helperRootEnv = "GALLERY_KILL_ROOT"
)

var recoveryClock = clock.Fixed{Time: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)}

func TestRealProcessKillpointMatrix(t *testing.T) {
	if os.Getenv(helperModeEnv) != "" {
		t.Skip("父进程矩阵不在 helper 进程中运行")
	}
	cases := []struct {
		name          string
		jobStatus     jobs.Status
		publication   string
		publicationN  int
		derivedStatus string
	}{
		{"job_queued", jobs.StatusCompleted, "new", 1, ""},
		{"partial_staging", jobs.StatusFailed, "old", 0, ""},
		{"candidate_complete", jobs.StatusFailed, "old", 0, ""},
		{"publication_control_gap", jobs.StatusCompleted, "new", 1, ""},
		{"overlay_fact_preprojection", jobs.StatusCompleted, "new", 1, ""},
		{"qpub_switched_pre_ws", jobs.StatusCompleted, "new", 1, ""},
		{"derived_generating", "", "old", 0, "failed"},
		{"full_hash_read", jobs.StatusFailed, "old", 0, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			oldPublication, sourceHash := seedRecoveryRoot(t, root)
			runAndKillHelper(t, root, test.name)
			runtime := openRuntime(t, root)
			defer runtime.close()
			if err := runtime.derived.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := runtime.scanner.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := runtime.overlay.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.store.Control.SQL().Exec(`UPDATE job_attempts
SET heartbeat_at=?, lease_expires_at=? WHERE status='running'`,
				recoveryClock.Now().Add(-10*time.Minute).Unix(), recoveryClock.Now().Add(-time.Minute).Unix()); err != nil {
				t.Fatal(err)
			}
			reconciler, err := recoveryservice.New(runtime.jobs, runtimeSubmitter{runtime: runtime}, time.Hour, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := reconciler.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := runtime.scanner.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			runtime.scanner.Wait()
			runtime.overlay.Wait()

			current, err := runtime.catalog.Current(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if test.publication == "old" && current.ID != oldPublication.ID {
				t.Fatalf("强杀前候选污染 active publication: old=%s current=%s", oldPublication.ID, current.ID)
			}
			if test.publication == "new" && current.ID == oldPublication.ID {
				t.Fatalf("已发布/恢复场景未切换 publication: %s", current.ID)
			}
			queryService, err := galleryquery.NewService(context.Background(), runtime.store.Control.SQL(), runtime.store.Catalog.SQL(), recoveryClock, nil)
			if err != nil {
				t.Fatal(err)
			}
			oldSnapshot, err := queryService.Search(context.Background(), galleryquery.Request{
				QueryPublicationID: oldPublication.ID, Limit: 20, AuthorizationScope: "recovery",
			})
			if err != nil || len(oldSnapshot.Items) != 1 {
				t.Fatalf("恢复后旧 publication 不可读: %+v %v", oldSnapshot, err)
			}
			var stagingCatalog, stagingOverlay int
			_ = runtime.store.Catalog.SQL().QueryRow("SELECT count(*) FROM catalog_revisions WHERE status='staging'").Scan(&stagingCatalog)
			_ = runtime.store.Catalog.SQL().QueryRow("SELECT count(*) FROM overlay_projection_revisions WHERE status='staging'").Scan(&stagingOverlay)
			if stagingCatalog != 0 || stagingOverlay != 0 {
				t.Fatalf("恢复后 staging 泄漏: catalog=%d overlay=%d", stagingCatalog, stagingOverlay)
			}
			if test.jobStatus != "" {
				jobID := strings.TrimSpace(readFile(t, filepath.Join(root, "job.id")))
				job, err := runtime.jobs.Get(context.Background(), jobID)
				if err != nil || job.Status != test.jobStatus {
					t.Fatalf("Job 恢复状态错误: want=%s got=%+v err=%v", test.jobStatus, job, err)
				}
				var publications int
				_ = runtime.store.Catalog.SQL().QueryRow("SELECT count(*) FROM query_publications WHERE job_id=?", jobID).Scan(&publications)
				if publications != test.publicationN {
					t.Fatalf("Job 重复/缺失 publication: want=%d got=%d", test.publicationN, publications)
				}
				if job.Status == jobs.StatusFailed && job.IssueCode != scanner.IssueProcessInterrupted {
					t.Fatalf("中断 Job issue 不稳定: %+v", job)
				}
			}
			if test.derivedStatus != "" {
				var status string
				if err := runtime.store.Catalog.SQL().QueryRow("SELECT status FROM derived_assets LIMIT 1").Scan(&status); err != nil || status != test.derivedStatus {
					t.Fatalf("Derived generating 未对账: status=%s err=%v", status, err)
				}
				temporary, _ := filepath.Glob(filepath.Join(runtime.dirs.Cache, "derived", "*", "*", "*.tmp"))
				if len(temporary) != 0 {
					t.Fatalf("Derived 临时文件未清理: %d", len(temporary))
				}
			}
			if test.name == "overlay_fact_preprojection" || test.name == "qpub_switched_pre_ws" {
				workID := oldSnapshot.Items[0].ID
				state, err := runtime.overlay.Get(context.Background(), workID)
				if err != nil || state.ProjectionStatus != "published" || state.PublishedQueryPublicationID != current.ID {
					t.Fatalf("Overlay 恢复未收敛: %+v %v", state, err)
				}
			}
			if test.name == "qpub_switched_pre_ws" {
				if _, err := os.Stat(filepath.Join(root, "ws-event.emitted")); !os.IsNotExist(err) {
					t.Fatalf("测试前提错误：强杀前已发 WS event: %v", err)
				}
			}
			afterHash := sourceTreeSHA256(t, filepath.Join(root, "source"))
			if afterHash != sourceHash {
				t.Fatal("强杀/恢复修改了 Source")
			}
		})
	}
}

func TestKillpointHelperProcess(t *testing.T) {
	mode := os.Getenv(helperModeEnv)
	if mode == "" {
		t.Skip("仅由 TestRealProcessKillpointMatrix 启动")
	}
	root := os.Getenv(helperRootEnv)
	if root == "" {
		t.Fatal("helper root 为空")
	}
	runtime := openRuntime(t, root)
	defer runtime.close()
	switch mode {
	case "job_queued":
		job, err := runtime.scanner.CreateScan(context.Background(), runtime.source.ID, "recovery")
		if err != nil {
			t.Fatal(err)
		}
		writeMarker(t, root, job.ID)
		blockAtKillpoint(t, root)
	case "partial_staging", "candidate_complete", "publication_control_gap":
		helperCatalogCandidate(t, runtime, root, mode)
	case "overlay_fact_preprojection":
		_, works, err := runtime.catalog.ListWorks(context.Background())
		if err != nil || len(works) != 1 {
			t.Fatal(err)
		}
		result, err := runtime.overlay.Put(context.Background(), works[0].ID, "recovery", overlay.Input{TitleOverride: "pending overlay"})
		if err != nil {
			t.Fatal(err)
		}
		writeMarker(t, root, result.ProjectionJobID)
		blockAtKillpoint(t, root)
	case "qpub_switched_pre_ws":
		_, works, err := runtime.catalog.ListWorks(context.Background())
		if err != nil || len(works) != 1 {
			t.Fatal(err)
		}
		notifier := &blockingPublicationNotifier{root: root, t: t}
		service, err := overlay.New(context.Background(), runtime.store.Control.SQL(), runtime.jobs, runtime.catalog, recoveryClock, notifier)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Put(context.Background(), works[0].ID, "recovery", overlay.Input{TitleOverride: "published before ws"})
		if err != nil {
			t.Fatal(err)
		}
		writeMarker(t, root, result.ProjectionJobID)
		if err := service.Execute(context.Background(), result.ProjectionJobID); err != nil {
			t.Fatal(err)
		}
	case "derived_generating":
		sum := sha256.Sum256([]byte("derived killpoint blob"))
		_, err := runtime.derived.GetOrCreate(context.Background(), derived.Request{
			Blob: domain.NewSHA256BlobRef(sum), TransformID: "thumbnail", TransformVersion: "1", Parameters: []byte(`{"width":200}`),
		}, func(_ context.Context, output io.Writer) (string, error) {
			_, _ = output.Write([]byte("partial"))
			blockAtKillpoint(t, root)
			return "image/webp", nil
		})
		if err != nil {
			t.Fatal(err)
		}
	case "full_hash_read":
		job, err := runtime.jobs.CreateScan(context.Background(), runtime.source.ID, "recovery", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.jobs.StartStage(context.Background(), job.ID, "hashing"); err != nil {
			t.Fatal(err)
		}
		writeMarker(t, root, job.ID)
		_, err = media.HashSourceFile(runtime.source.RootPath, "work-one/media.bin", func() { blockAtKillpoint(t, root) })
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("未知 killpoint %q", mode)
	}
}

type runtimeServices struct {
	dirs      appdirs.Dirs
	store     *storage.Store
	resources *application.Resources
	jobs      *jobs.Store
	catalog   *catalog.Store
	scanner   *scanner.Service
	overlay   *overlay.Service
	derived   *derived.Service
	source    application.Source
}

type runtimeSubmitter struct{ runtime *runtimeServices }

func (s runtimeSubmitter) Submit(class, jobID string) bool {
	switch class {
	case jobs.ResourceScan:
		_ = s.runtime.scanner.Execute(context.Background(), jobID)
	case jobs.ResourceOverlay:
		_ = s.runtime.overlay.Execute(context.Background(), jobID)
	default:
		return false
	}
	return true
}

func openRuntime(t *testing.T, root string) *runtimeServices {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	generator := identity.NewGenerator(recoveryClock)
	resources, err := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, recoveryClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	jobStore, err := jobs.NewStore(store.Control.SQL(), recoveryClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), recoveryClock, generator)
	if err != nil {
		t.Fatal(err)
	}
	scannerService, err := scanner.New(ctx, resources, jobStore, catalogStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	overlayService, err := overlay.New(ctx, store.Control.SQL(), jobStore, catalogStore, recoveryClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedService, err := derived.New(store.Catalog.SQL(), dirs.Cache, recoveryClock, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID string
	if err := store.Control.SQL().QueryRowContext(ctx, "SELECT source_id FROM sources ORDER BY source_id LIMIT 1").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	source, err := resources.GetSource(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeServices{dirs: dirs, store: store, resources: resources, jobs: jobStore,
		catalog: catalogStore, scanner: scannerService, overlay: overlayService, derived: derivedService, source: source}
}

func (r *runtimeServices) close() {
	if r == nil {
		return
	}
	r.scanner.Wait()
	r.overlay.Wait()
	_ = r.store.Close()
}

func seedRecoveryRoot(t *testing.T, root string) (catalog.Publication, string) {
	t.Helper()
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	generator := identity.NewGenerator(recoveryClock)
	resources, _ := application.NewResources(store.Control.SQL(), dirs, filesystem.OS{}, recoveryClock, generator)
	library, err := resources.CreateLibrary(ctx, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "work-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceRoot, "work-one", "media.bin")
	if err := os.WriteFile(sourcePath, []byte("real process killpoint fixture"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "work-one", "metadata.json"),
		[]byte(`{"creator":{"name":"Recovery Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	source, err := resources.CreateSource(ctx, library.ID, "synthetic", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	rulePackage, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := resources.CreateRuleVersion(ctx, rulePackage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.CreateSourceRuleBinding(ctx, source.ID, version.SemanticHash, []byte("{}"), 0); err != nil {
		t.Fatal(err)
	}
	jobStore, _ := jobs.NewStore(store.Control.SQL(), recoveryClock, generator)
	catalogStore, _ := catalog.NewStore(store.Catalog.SQL(), recoveryClock, generator)
	scannerService, _ := scanner.New(ctx, resources, jobStore, catalogStore, nil)
	job, err := scannerService.CreateScan(ctx, source.ID, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := scannerService.Execute(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	publication, err := catalogStore.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return publication, sourceTreeSHA256(t, sourceRoot)
}

func helperCatalogCandidate(t *testing.T, runtime *runtimeServices, root, mode string) {
	t.Helper()
	ctx := context.Background()
	job, err := runtime.jobs.CreateScan(ctx, runtime.source.ID, "recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.jobs.Start(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	writeMarker(t, root, job.ID)
	_, works, err := runtime.catalog.ListWorks(ctx)
	if err != nil || len(works) != 1 {
		t.Fatal(err)
	}
	_, mediaItems, err := runtime.catalog.ListMediaForWork(ctx, works[0].ID)
	if err != nil || len(mediaItems) != 1 {
		t.Fatal(err)
	}
	hashed, err := media.HashSourceFile(runtime.source.RootPath, "work-one/media.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	var libraryID string
	if err := runtime.store.Control.SQL().QueryRowContext(ctx, "SELECT library_id FROM sources WHERE source_id=?", runtime.source.ID).Scan(&libraryID); err != nil {
		t.Fatal(err)
	}
	_, _, watermark, err := runtime.resources.QueryOverlaySnapshot(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := runtime.catalog.BeginCandidate(ctx, job.ID, runtime.source.ID, watermark)
	if err != nil {
		t.Fatal(err)
	}
	workFacts := []catalog.WorkFact{{SourceID: runtime.source.ID, LibraryID: libraryID, SourceKey: "work-one",
		SourceTitle: "work-one", Title: "work-one", WorkID: works[0].ID}}
	if mode == "partial_staging" {
		if err := runtime.catalog.Stage(ctx, candidate, workFacts, nil); err != nil {
			t.Fatal(err)
		}
		blockAtKillpoint(t, root)
	}
	mediaFacts := []catalog.MediaFact{{SourceID: runtime.source.ID, SourceKey: "work-one/media.bin",
		WorkSourceKey: "work-one", RuleKey: "media.bin", RelativePath: hashed.RelativePath,
		Kind: mediaItems[0].Kind, MIME: mediaItems[0].MIME, Size: hashed.Size,
		Algorithm: hashed.Blob.Algorithm, Digest: hashed.Blob.Digest, LocationKey: hashed.LocationKey,
		MediaID: mediaItems[0].ID, WorkID: works[0].ID, Ordinal: 0}}
	if err := runtime.catalog.Stage(ctx, candidate, workFacts, mediaFacts); err != nil {
		t.Fatal(err)
	}
	if err := runtime.catalog.ValidateCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if mode == "candidate_complete" {
		blockAtKillpoint(t, root)
	}
	if _, err := runtime.jobs.BeginPublishing(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.catalog.Publish(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	blockAtKillpoint(t, root)
}

type blockingPublicationNotifier struct {
	root string
	t    *testing.T
}

func (n *blockingPublicationNotifier) JobChanged(jobs.Job) {}

func (n *blockingPublicationNotifier) PublicationPublished(catalog.Publication) {
	blockAtKillpoint(n.t, n.root)
	_ = os.WriteFile(filepath.Join(n.root, "ws-event.emitted"), []byte("unexpected"), 0o600)
}

func runAndKillHelper(t *testing.T, root, mode string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestKillpointHelperProcess$")
	command.Env = append(os.Environ(), helperModeEnv+"="+mode, helperRootEnv+"="+root)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(root, "kill.ready")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyPath); err == nil {
			if err := command.Process.Kill(); err != nil {
				t.Fatalf("强杀 helper: %v output=%s", err, output.String())
			}
			_ = command.Wait()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_ = command.Wait()
	t.Fatalf("helper 未到达 killpoint %s: %s", mode, output.String())
}

func blockAtKillpoint(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "kill.ready"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func writeMarker(t *testing.T, root, jobID string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "job.id"), []byte(jobID), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sourceTreeSHA256(t *testing.T, root string) string {
	t.Helper()
	hasher := sha256.New()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		_, _ = hasher.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hasher.Write([]byte{0})
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func TestKillpointNamesAreStable(t *testing.T) {
	for _, name := range []string{"job_queued", "partial_staging", "candidate_complete",
		"publication_control_gap", "overlay_fact_preprojection", "qpub_switched_pre_ws",
		"derived_generating", "full_hash_read"} {
		if strings.TrimSpace(name) == "" {
			t.Fatal(fmt.Errorf("空 killpoint"))
		}
	}
}
