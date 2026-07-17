package bootstrap_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/bootstrap"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

func TestRunnableGallerydUsesAppDirsAndLeavesSyntheticSourceUnchanged(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(source, "media.bin")
	content := []byte("synthetic read-only media")
	if err := os.WriteFile(sentinel, content, 0o600); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(content)
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs, SourceRoots: []string{source}}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(ctx, cfg, logger) }()

	descriptorPath := filepath.Join(dirs.Runtime, "galleryd.json")
	runtimeDescriptor := waitForDescriptor(t, descriptorPath)
	client, err := api.NewClientWithResponses("http://" + runtimeDescriptor.Address)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response, err := client.GetHealthWithResponse(context.Background())
	if err != nil || response.JSON200 == nil {
		cancel()
		t.Fatalf("运行中的 galleryd health 失败: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("galleryd 未在期限内优雅停止")
	}

	afterContent, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	after := sha256.Sum256(afterContent)
	if before != after {
		t.Fatal("合成 Source 在启动/停止后发生变化")
	}
	for _, name := range []string{"control.db", "catalog.db"} {
		if _, err := os.Stat(filepath.Join(dirs.Data, name)); err != nil {
			t.Fatalf("AppDirs 数据库未创建: %v", err)
		}
	}
	if _, err := os.Stat(descriptorPath); !os.IsNotExist(err) {
		t.Fatal("停止后 runtime descriptor 未清理")
	}
}

func TestWalkingSkeletonPersistsAcrossRealGallerydRestart(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	work := filepath.Join(source, "work-one")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "walking-skeleton", "work-one", "media.bin"))
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(work, "media.bin")
	if err := os.WriteFile(mediaPath, fixture, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "metadata.json"), []byte(`{"creator":{"name":"Bootstrap Creator"}}`), 0o400); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(mustReadFile(t, mediaPath))
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs, SourceRoots: []string{source}}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	firstCancel, firstDone, firstDescriptor := startGalleryd(t, cfg, logger)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Jar: jar}
	client := newAPIClient(t, "http://"+firstDescriptor.Address, httpClient)
	bootstrapResponse, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || bootstrapResponse.JSON200 == nil {
		t.Fatalf("bootstrap: %v", err)
	}
	editor := originEditor("http://" + firstDescriptor.Address)
	attempt, err := client.CreatePairingAttemptWithResponse(context.Background(), &api.CreatePairingAttemptParams{XGalleryCSRF: bootstrapResponse.JSON200.CsrfToken}, editor)
	if err != nil || attempt.JSON201 == nil {
		t.Fatalf("pair attempt: %v status=%d", err, attempt.StatusCode())
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(context.Background(), &api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrapResponse.JSON200.CsrfToken}, api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, editor)
	if err != nil || exchange.JSON201 == nil {
		t.Fatalf("pair exchange: %v status=%d", err, exchange.StatusCode())
	}
	csrf := exchange.JSON201.CsrfToken
	library, err := client.CreateLibraryWithResponse(context.Background(), &api.CreateLibraryParams{XGalleryCSRF: csrf}, api.LibraryCreateRequest{Name: "Restart"}, editor)
	if err != nil || library.JSON201 == nil {
		t.Fatalf("library: %v status=%d", err, library.StatusCode())
	}
	sourceResponse, err := client.CreateSourceWithResponse(context.Background(), &api.CreateSourceParams{XGalleryCSRF: csrf}, api.SourceCreateRequest{LibraryId: library.JSON201.Id, DisplayName: "Synthetic", RootPath: source}, editor)
	if err != nil || sourceResponse.JSON201 == nil {
		t.Fatalf("source: %v status=%d body=%s", err, sourceResponse.StatusCode(), sourceResponse.Body)
	}
	ruleBytes, err := os.ReadFile(filepath.Join("..", "rules", "testdata", "minimal-rule-package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rulePackage map[string]any
	if err := json.Unmarshal(ruleBytes, &rulePackage); err != nil {
		t.Fatal(err)
	}
	rule, err := client.CreateRuleVersionWithResponse(context.Background(), &api.CreateRuleVersionParams{XGalleryCSRF: csrf}, api.RuleVersionCreateRequest{Package: rulePackage}, editor)
	if err != nil || rule.JSON201 == nil {
		t.Fatalf("rule: %v status=%d body=%s", err, rule.StatusCode(), rule.Body)
	}
	binding, err := client.CreateSourceRuleBindingWithResponse(context.Background(), &api.CreateSourceRuleBindingParams{XGalleryCSRF: csrf}, api.SourceRuleBindingCreateRequest{SourceId: sourceResponse.JSON201.Id, SemanticHash: rule.JSON201.SemanticHash, Parameters: map[string]any{}, Priority: 0}, editor)
	if err != nil || binding.JSON201 == nil {
		t.Fatalf("binding: %v status=%d body=%s", err, binding.StatusCode(), binding.Body)
	}
	scan, err := client.CreateScanJobWithResponse(context.Background(), sourceResponse.JSON201.Id, &api.CreateScanJobParams{XGalleryCSRF: csrf}, editor)
	if err != nil || scan.JSON202 == nil {
		t.Fatalf("scan: %v status=%d", err, scan.StatusCode())
	}
	job := waitJob(t, client, scan.JSON202.Id)
	if string(job.Status) != "completed" || job.QueryPublicationId == nil {
		t.Fatalf("job: %+v", job)
	}
	publication, err := client.GetCurrentQueryPublicationWithResponse(context.Background())
	if err != nil || publication.JSON200 == nil {
		t.Fatalf("publication: %v status=%d", err, publication.StatusCode())
	}
	publicationID := publication.JSON200.Id
	stopGalleryd(t, firstCancel, firstDone)

	secondCancel, secondDone, secondDescriptor := startGalleryd(t, cfg, logger)
	client = newAPIClient(t, "http://"+secondDescriptor.Address, httpClient)
	afterRestart, err := client.GetBootstrapWithResponse(context.Background())
	if err != nil || afterRestart.JSON200 == nil || !afterRestart.JSON200.Authenticated {
		t.Fatalf("重启后 Session 未恢复: %v %+v", err, afterRestart.JSON200)
	}
	reloadedJob, err := client.GetJobWithResponse(context.Background(), job.Id)
	if err != nil || reloadedJob.JSON200 == nil || string(reloadedJob.JSON200.Status) != "completed" {
		t.Fatalf("重启后 Job 未恢复: %v status=%d", err, reloadedJob.StatusCode())
	}
	reloadedPublication, err := client.GetCurrentQueryPublicationWithResponse(context.Background())
	if err != nil || reloadedPublication.JSON200 == nil || reloadedPublication.JSON200.Id != publicationID {
		t.Fatalf("重启后 publication 漂移: %v %+v", err, reloadedPublication.JSON200)
	}
	works, err := client.ListWorksWithResponse(context.Background(), nil)
	if err != nil || works.JSON200 == nil || len(works.JSON200.Works) != 1 {
		t.Fatalf("重启后 Work 查询失败: %v status=%d", err, works.StatusCode())
	}
	mediaItems, err := client.ListWorkMediaWithResponse(context.Background(), works.JSON200.Works[0].Id)
	if err != nil || mediaItems.JSON200 == nil || len(mediaItems.JSON200.Media) != 1 {
		t.Fatalf("重启后 Media 查询失败: %v status=%d", err, mediaItems.StatusCode())
	}
	content, err := client.GetMediaContentWithResponse(context.Background(), mediaItems.JSON200.Media[0].Id, &api.GetMediaContentParams{})
	if err != nil || content.StatusCode() != http.StatusOK || string(content.Body) != string(fixture) {
		t.Fatalf("重启后媒体读取失败: %v status=%d", err, content.StatusCode())
	}
	stopGalleryd(t, secondCancel, secondDone)
	after := sha256.Sum256(mustReadFile(t, mediaPath))
	if before != after {
		t.Fatal("真实进程完整流程修改了 Source")
	}
}

func startGalleryd(t *testing.T, cfg config.Config, logger *slog.Logger) (context.CancelFunc, <-chan error, descriptor.Descriptor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bootstrap.Run(ctx, cfg, logger) }()
	value := waitForDescriptor(t, filepath.Join(cfg.AppDirs.Runtime, "galleryd.json"))
	return cancel, done, value
}

func stopGalleryd(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("galleryd 未在期限内停止")
	}
}

func newAPIClient(t *testing.T, baseURL string, httpClient *http.Client) *api.ClientWithResponses {
	t.Helper()
	client, err := api.NewClientWithResponses(baseURL, api.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func originEditor(origin string) api.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
}

func waitJob(t *testing.T, client *api.ClientWithResponses, jobID string) api.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.GetJobWithResponse(context.Background(), jobID)
		if err != nil || response.JSON200 == nil {
			t.Fatalf("job snapshot: %v status=%d", err, response.StatusCode())
		}
		if status := string(response.JSON200.Status); status == "completed" || status == "failed" || status == "cancelled" || status == "needs_repair" {
			return *response.JSON200
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Job 未终止")
	return api.Job{}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestOverlapFailsBeforeDatabaseInitialization(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	source := filepath.Join(dirs.Data, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs, SourceRoots: []string{source}}
	err := bootstrap.Run(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("重叠 Source 启动成功")
	}
	if _, err := os.Stat(filepath.Join(dirs.Data, "control.db")); !os.IsNotExist(err) {
		t.Fatal("重叠守卫失败前已初始化数据库")
	}
}

func TestSecondInstanceRejectedAndLockReleasedOnStop(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.UnderRoot(filepath.Join(root, "app"))
	cfg := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: dirs}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// 第一个实例取得所有权并进入服务状态。
	firstCancel, firstDone, firstDescriptor := startGalleryd(t, cfg, logger)
	descriptorPath := filepath.Join(dirs.Runtime, "galleryd.json")

	// 第二个实例竞争同一 AppDirs：必须以 INSTANCE_ALREADY_RUNNING 失败。
	secondErr := bootstrap.Run(context.Background(), cfg, logger)
	var structured *fault.Error
	if !errors.As(secondErr, &structured) || structured.Code != fault.CodeInstanceAlreadyRunning {
		firstCancel()
		<-firstDone
		t.Fatalf("第二实例未因已有实例失败: %v", secondErr)
	}
	// 第二实例不得改写活动实例的 descriptor（nonce 不变），第一实例仍健康。
	after := waitForDescriptor(t, descriptorPath)
	if after.StartupNonce != firstDescriptor.StartupNonce {
		firstCancel()
		<-firstDone
		t.Fatal("第二实例改写了活动 descriptor")
	}
	client, err := api.NewClientWithResponses("http://" + firstDescriptor.Address)
	if err != nil {
		firstCancel()
		<-firstDone
		t.Fatal(err)
	}
	if health, err := client.GetHealthWithResponse(context.Background()); err != nil || health.JSON200 == nil {
		firstCancel()
		<-firstDone
		t.Fatalf("第二实例失败后第一实例不健康: %v", err)
	}

	// 第一实例优雅停止后释放锁，新的实例可以取得所有权。
	stopGalleryd(t, firstCancel, firstDone)
	thirdCancel, thirdDone, _ := startGalleryd(t, cfg, logger)
	stopGalleryd(t, thirdCancel, thirdDone)
}

func TestDifferentAppDirsRunConcurrently(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfgA := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: appdirs.UnderRoot(filepath.Join(root, "a"))}
	cfgB := config.Config{Mode: config.ModePersonal, Listen: "127.0.0.1:0", AppDirs: appdirs.UnderRoot(filepath.Join(root, "b"))}
	aCancel, aDone, aDescriptor := startGalleryd(t, cfgA, logger)
	bCancel, bDone, bDescriptor := startGalleryd(t, cfgB, logger)
	if aDescriptor.Address == bDescriptor.Address {
		aCancel()
		bCancel()
		<-aDone
		<-bDone
		t.Fatal("两个不同 AppDirs 实例共用地址")
	}
	stopGalleryd(t, aCancel, aDone)
	stopGalleryd(t, bCancel, bDone)
}

func waitForDescriptor(t *testing.T, path string) descriptor.Descriptor {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil {
			var value descriptor.Descriptor
			if err := json.Unmarshal(content, &value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("未等待到 runtime descriptor")
	return descriptor.Descriptor{}
}
