// Command historical-upgrade 使用来自两个真实 Git 提交的 Windows galleryd 二进制，
// 验证旧 control schema 的前向迁移、用户事实承接，以及旧程序对新 schema 的拒绝。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	api "github.com/RecRivenVI/gallery/api"
	testprocess "github.com/RecRivenVI/gallery/tools/testlab/internal/process"
)

const (
	beforeUpgradeLibrary = "Historical upgrade persistent fact"
	afterUpgradeLibrary  = "Historical upgrade current fact"
	historicalTokenName  = "Historical upgrade persistent credential"
	startupTimeout       = 60 * time.Second
	jobTimeout           = 30 * time.Second
)

type pairedClient struct {
	api    *api.ClientWithResponses
	csrf   string
	editor api.RequestEditorFn
}

type result struct {
	HistoricalCommit               string `json:"historicalCommit"`
	CurrentCommit                  string `json:"currentCommit"`
	HistoricalSchemaVersion        int64  `json:"historicalSchemaVersion"`
	CurrentSchemaVersion           int64  `json:"currentSchemaVersion"`
	HistoricalBackupSchemaVersion  int64  `json:"historicalBackupSchemaVersion"`
	CurrentBackupSchemaVersion     int64  `json:"currentBackupSchemaVersion"`
	RestoreWillMigrate             bool   `json:"restoreWillMigrate"`
	UpgradePreservedFacts          bool   `json:"upgradePreservedFacts"`
	UpgradePreservedCredential     bool   `json:"upgradePreservedCredential"`
	DowngradeRejected              bool   `json:"downgradeRejected"`
	DowngradeLeftDatabaseUntouched bool   `json:"downgradeLeftDatabaseUntouched"`
	DowngradePreservedCredential   bool   `json:"downgradePreservedCredential"`
	CurrentRestartedAfterDowngrade bool   `json:"currentRestartedAfterDowngrade"`
	AllNormalStopsExitedGracefully bool   `json:"allNormalStopsExitedGracefully"`
}

type persistentToken struct {
	ID     string
	Secret string
}

type controlFileFact struct {
	Name   string
	Length int64
	SHA256 string
}

func main() {
	historicalBin := flag.String("historical-bin", "", "真实历史提交构建的 galleryd.exe")
	currentBin := flag.String("current-bin", "", "当前提交构建的 galleryd.exe")
	historicalCommit := flag.String("historical-commit", "", "历史二进制的完整 Git commit")
	currentCommit := flag.String("current-commit", "", "当前二进制的完整 Git commit")
	historicalSchema := flag.Int64("historical-schema", 0, "历史 control schema 版本")
	currentSchema := flag.Int64("current-schema", 0, "当前 control schema 版本")
	flag.Parse()

	if err := run(*historicalBin, *currentBin, *historicalCommit, *currentCommit, *historicalSchema, *currentSchema); err != nil {
		fmt.Fprintf(os.Stderr, "historical upgrade smoke 失败：%v\n", err)
		os.Exit(1)
	}
}

func run(historicalBin, currentBin, historicalCommit, currentCommit string, historicalSchema, currentSchema int64) error {
	if err := requireSupportedPlatform(); err != nil {
		return err
	}
	historicalPath, currentPath, err := validateInputs(
		historicalBin, currentBin, historicalCommit, currentCommit, historicalSchema, currentSchema,
	)
	if err != nil {
		return err
	}

	testRoot, err := os.MkdirTemp("", "gallery-historical-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(testRoot)
	logs := filepath.Join(testRoot, "logs")
	appRoot := filepath.Join(testRoot, "appdirs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stopsGraceful := true
	var active *testprocess.Process
	defer func() {
		if active != nil {
			active.Stop()
		}
	}()

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, historicalPath, appRoot, filepath.Join(logs, "historical.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("启动真实历史版本: %w", err)
	}
	historicalClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("历史版本配对: %w", err)
	}
	if err := createLibrary(ctx, historicalClient, beforeUpgradeLibrary); err != nil {
		return err
	}
	historicalToken, err := createPersistentToken(ctx, historicalClient)
	if err != nil {
		return fmt.Errorf("历史版本创建 API Token: %w", err)
	}
	historicalBackup, err := createBackup(ctx, historicalClient)
	if err != nil {
		return fmt.Errorf("历史版本创建 control 备份: %w", err)
	}
	if historicalBackup.SchemaVersion != historicalSchema {
		return fmt.Errorf("历史备份 schema=%d，预期 %d", historicalBackup.SchemaVersion, historicalSchema)
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("历史版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-upgrade.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("当前版本迁移历史数据库: %w", err)
	}
	currentClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("迁移后当前版本配对: %w", err)
	}
	if err := assertLibraries(ctx, currentClient, map[string]bool{beforeUpgradeLibrary: true}); err != nil {
		return fmt.Errorf("迁移后的历史用户事实: %w", err)
	}
	if err := assertPersistentToken(ctx, active.BaseURL, currentClient, historicalToken); err != nil {
		return fmt.Errorf("迁移后的历史凭据: %w", err)
	}
	verify, err := verifyBackup(ctx, currentClient, historicalBackup.BackupId)
	if err != nil {
		return err
	}
	if !verify.Compatible || !verify.WillMigrate || !verify.ChecksumVerified || !verify.IntegrityOk ||
		!verify.InvariantsOk || verify.BackupSchemaVersion != historicalSchema ||
		verify.CurrentSchemaVersion != currentSchema {
		return fmt.Errorf("历史备份迁移 dry-run 未返回精确成功事实")
	}
	if err := createLibrary(ctx, currentClient, afterUpgradeLibrary); err != nil {
		return err
	}
	currentBackup, err := createBackup(ctx, currentClient)
	if err != nil {
		return fmt.Errorf("当前版本创建 control 备份: %w", err)
	}
	if currentBackup.SchemaVersion != currentSchema {
		return fmt.Errorf("当前备份 schema=%d，预期 %d", currentBackup.SchemaVersion, currentSchema)
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("迁移后当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	beforeDowngrade, err := sealControlDatabase(appRoot)
	if err != nil {
		return err
	}
	downgradeLog := filepath.Join(logs, "historical-downgrade-rejection.log")
	unexpected, downgradeErr := testprocess.StartGallerydWithSourceRootsContext(
		ctx, historicalPath, appRoot, downgradeLog, startupTimeout,
	)
	if unexpected != nil {
		unexpected.Stop()
		return fmt.Errorf("历史版本错误接受了当前 schema")
	}
	if downgradeErr == nil {
		return fmt.Errorf("历史版本降级尝试没有返回失败")
	}
	if err := assertDowngradeLog(downgradeLog, historicalSchema+1); err != nil {
		return err
	}
	afterDowngrade, err := sealControlDatabase(appRoot)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeDowngrade, afterDowngrade) {
		return fmt.Errorf("拒绝降级时 control 数据库字节事实发生变化")
	}

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-downgrade-rejection.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("拒绝降级后重启当前版本: %w", err)
	}
	restartedClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("拒绝降级后当前版本配对: %w", err)
	}
	if err := assertLibraries(ctx, restartedClient, map[string]bool{
		beforeUpgradeLibrary: true,
		afterUpgradeLibrary:  true,
	}); err != nil {
		return fmt.Errorf("拒绝降级后的用户事实: %w", err)
	}
	if err := assertPersistentToken(ctx, active.BaseURL, restartedClient, historicalToken); err != nil {
		return fmt.Errorf("拒绝降级后的历史凭据: %w", err)
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("拒绝降级后当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	encoded, err := json.Marshal(result{
		HistoricalCommit:               historicalCommit,
		CurrentCommit:                  currentCommit,
		HistoricalSchemaVersion:        historicalSchema,
		CurrentSchemaVersion:           currentSchema,
		HistoricalBackupSchemaVersion:  historicalBackup.SchemaVersion,
		CurrentBackupSchemaVersion:     currentBackup.SchemaVersion,
		RestoreWillMigrate:             true,
		UpgradePreservedFacts:          true,
		UpgradePreservedCredential:     true,
		DowngradeRejected:              true,
		DowngradeLeftDatabaseUntouched: true,
		DowngradePreservedCredential:   true,
		CurrentRestartedAfterDowngrade: true,
		AllNormalStopsExitedGracefully: stopsGraceful,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func validateInputs(historicalBin, currentBin, historicalCommit, currentCommit string, historicalSchema, currentSchema int64) (string, string, error) {
	if historicalBin == "" || currentBin == "" {
		return "", "", fmt.Errorf("必须完整指定历史与当前二进制")
	}
	if !isFullCommit(historicalCommit) || !isFullCommit(currentCommit) || historicalCommit == currentCommit {
		return "", "", fmt.Errorf("必须指定两个不同的小写完整 Git commit")
	}
	if historicalSchema < 1 || currentSchema <= historicalSchema {
		return "", "", fmt.Errorf("当前 schema 必须严格高于正数历史 schema")
	}
	historicalPath, err := filepath.Abs(historicalBin)
	if err != nil {
		return "", "", err
	}
	currentPath, err := filepath.Abs(currentBin)
	if err != nil {
		return "", "", err
	}
	if strings.EqualFold(historicalPath, currentPath) {
		return "", "", fmt.Errorf("历史与当前版本不能复用同一个二进制")
	}
	for _, path := range []string{historicalPath, currentPath} {
		if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("二进制不存在或不是普通文件")
		}
	}
	return historicalPath, currentPath, nil
}

func isFullCommit(value string) bool {
	if len(value) != 40 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func pair(ctx context.Context, baseURL string) (*pairedClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client, err := api.NewClientWithResponses(baseURL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		return nil, err
	}
	editor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", baseURL)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
	bootstrap, err := client.GetBootstrapWithResponse(ctx, editor)
	if err != nil || bootstrap.JSON200 == nil {
		return nil, fmt.Errorf("读取 bootstrap：status=%d err=%v", statusCode(bootstrap), err)
	}
	attempt, err := client.CreatePairingAttemptWithResponse(
		ctx,
		&api.CreatePairingAttemptParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken},
		editor,
	)
	if err != nil || attempt.JSON201 == nil {
		return nil, fmt.Errorf("创建配对凭据：status=%d err=%v", statusCode(attempt), err)
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(
		ctx,
		&api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken},
		api.PairingExchangeRequest{Credential: attempt.JSON201.Credential},
		editor,
	)
	if err != nil || exchange.JSON201 == nil {
		return nil, fmt.Errorf("交换配对凭据：status=%d err=%v", statusCode(exchange), err)
	}
	return &pairedClient{api: client, csrf: exchange.JSON201.CsrfToken, editor: editor}, nil
}

func createLibrary(ctx context.Context, client *pairedClient, name string) error {
	response, err := client.api.CreateLibraryWithResponse(
		ctx,
		&api.CreateLibraryParams{XGalleryCSRF: client.csrf},
		api.LibraryCreateRequest{Name: name},
		client.editor,
	)
	if err != nil || response.JSON201 == nil || response.JSON201.Name != name {
		return fmt.Errorf("创建 Library %q：status=%d err=%v", name, statusCode(response), err)
	}
	return nil
}

func createPersistentToken(ctx context.Context, client *pairedClient) (persistentToken, error) {
	response, err := client.api.CreateAPITokenWithResponse(
		ctx,
		&api.CreateAPITokenParams{XGalleryCSRF: client.csrf},
		api.APITokenCreateRequest{
			Name:         historicalTokenName,
			Capabilities: []string{"library.read"},
			Scopes:       []api.ResourceScope{{Kind: api.ResourceScopeKindGlobal}},
		},
		client.editor,
	)
	if err != nil || response.JSON201 == nil || response.JSON201.Id == "" || response.JSON201.Secret == "" {
		return persistentToken{}, fmt.Errorf("创建 API Token：status=%d err=%v", statusCode(response), err)
	}
	return persistentToken{ID: response.JSON201.Id, Secret: response.JSON201.Secret}, nil
}

func assertPersistentToken(ctx context.Context, baseURL string, client *pairedClient, expected persistentToken) error {
	response, err := client.api.ListAPITokensWithResponse(ctx, nil, client.editor)
	if err != nil || response.JSON200 == nil {
		return fmt.Errorf("列出 API Token：status=%d err=%v", statusCode(response), err)
	}
	found := false
	for _, token := range response.JSON200.Tokens {
		if token.Id != expected.ID {
			continue
		}
		if token.Name != historicalTokenName || token.Revoked ||
			!reflect.DeepEqual(token.Capabilities, []string{"library.read"}) ||
			len(token.Scopes) != 1 || token.Scopes[0].Kind != api.ResourceScopeKindGlobal || token.Scopes[0].Id != nil {
			return fmt.Errorf("API Token 持久事实不一致")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("没有找到历史 API Token")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/bootstrap", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+expected.Secret)
	bearerResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("Bearer Token 请求: %w", err)
	}
	defer bearerResponse.Body.Close()
	if bearerResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("Bearer Token status=%d", bearerResponse.StatusCode)
	}
	var bootstrap api.BootstrapResponse
	if err := json.NewDecoder(bearerResponse.Body).Decode(&bootstrap); err != nil {
		return fmt.Errorf("解码 Bearer bootstrap: %w", err)
	}
	if !bootstrap.Authenticated {
		return fmt.Errorf("历史 API Token 未通过真实 Bearer 认证")
	}
	return nil
}

func createBackup(ctx context.Context, client *pairedClient) (api.ControlBackupManifest, error) {
	before, err := client.api.ListControlBackupsWithResponse(ctx, client.editor)
	if err != nil || before.JSON200 == nil {
		return api.ControlBackupManifest{}, fmt.Errorf("读取备份清单：status=%d err=%v", statusCode(before), err)
	}
	existing := make(map[string]struct{}, len(before.JSON200.Backups))
	for _, item := range before.JSON200.Backups {
		existing[item.BackupId] = struct{}{}
	}
	created, err := client.api.CreateControlBackupWithResponse(
		ctx,
		&api.CreateControlBackupParams{XGalleryCSRF: client.csrf},
		client.editor,
	)
	if err != nil || created.JSON202 == nil {
		return api.ControlBackupManifest{}, fmt.Errorf("创建 control 备份：status=%d err=%v", statusCode(created), err)
	}
	if err := waitJob(ctx, client, created.JSON202.Id); err != nil {
		return api.ControlBackupManifest{}, err
	}
	after, err := client.api.ListControlBackupsWithResponse(ctx, client.editor)
	if err != nil || after.JSON200 == nil {
		return api.ControlBackupManifest{}, fmt.Errorf("重新读取备份清单：status=%d err=%v", statusCode(after), err)
	}
	var found []api.ControlBackupManifest
	for _, item := range after.JSON200.Backups {
		if _, ok := existing[item.BackupId]; !ok {
			found = append(found, item)
		}
	}
	if len(found) != 1 {
		return api.ControlBackupManifest{}, fmt.Errorf("预期恰好一个新备份，实际为 %d", len(found))
	}
	return found[0], nil
}

func verifyBackup(ctx context.Context, client *pairedClient, backupID string) (api.ControlRestoreReport, error) {
	response, err := client.api.VerifyControlRestoreWithResponse(
		ctx,
		&api.VerifyControlRestoreParams{XGalleryCSRF: client.csrf},
		api.ControlRestoreRequest{BackupId: backupID},
		client.editor,
	)
	if err != nil || response.JSON200 == nil {
		return api.ControlRestoreReport{}, fmt.Errorf("验证历史 control 备份：status=%d err=%v", statusCode(response), err)
	}
	return *response.JSON200, nil
}

func waitJob(parent context.Context, client *pairedClient, jobID string) error {
	ctx, cancel := context.WithTimeout(parent, jobTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.api.GetJobWithResponse(ctx, jobID, client.editor)
		if err != nil || response.JSON200 == nil {
			return fmt.Errorf("读取备份 Job：status=%d err=%v", statusCode(response), err)
		}
		switch string(response.JSON200.Status) {
		case "completed":
			return nil
		case "failed", "cancelled", "superseded", "needs_repair":
			return fmt.Errorf("备份 Job 未成功：status=%s", response.JSON200.Status)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待备份 Job: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertLibraries(ctx context.Context, client *pairedClient, expected map[string]bool) error {
	response, err := client.api.ListLibrariesWithResponse(ctx, client.editor)
	if err != nil || response.JSON200 == nil {
		return fmt.Errorf("读取 Library：status=%d err=%v", statusCode(response), err)
	}
	actual := make(map[string]bool, len(response.JSON200.Libraries))
	for _, library := range response.JSON200.Libraries {
		actual[library.Name] = true
	}
	for name, want := range expected {
		if actual[name] != want {
			return fmt.Errorf("Library %q 存在性不匹配：got=%t want=%t", name, actual[name], want)
		}
	}
	return nil
}

func sealControlDatabase(appRoot string) ([]controlFileFact, error) {
	var facts []controlFileFact
	for _, name := range []string{"control.db", "control.db-shm", "control.db-wal"} {
		path := filepath.Join(appRoot, "data", name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取 control 数据库封印目标: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("control 数据库封印目标不是普通文件")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取 control 数据库封印内容: %w", err)
		}
		digest := sha256.Sum256(body)
		facts = append(facts, controlFileFact{Name: name, Length: info.Size(), SHA256: hex.EncodeToString(digest[:])})
	}
	if len(facts) == 0 {
		return nil, fmt.Errorf("没有找到 control 数据库")
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].Name < facts[j].Name })
	return facts, nil
}

func assertDowngradeLog(path string, currentSchema int64) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取降级拒绝日志: %w", err)
	}
	want := "未知的 migration version " + strconv.FormatInt(currentSchema, 10)
	if !strings.Contains(string(body), want) {
		return fmt.Errorf("历史版本没有以未知新 migration 拒绝降级")
	}
	return nil
}

type responseWithStatus interface {
	StatusCode() int
}

func statusCode(response responseWithStatus) int {
	if response == nil {
		return 0
	}
	value := reflect.ValueOf(response)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return 0
	}
	return response.StatusCode()
}
