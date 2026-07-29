// Command portable-upgrade 使用两个正式 Windows 便携包中的 galleryd 二进制，
// 验证程序/数据分离、跨产品版本标签重启、control 备份验证与待重启恢复。
// 两个二进制可以来自同一源码提交；这种模式只证明制品编排，不冒充真实历史 Schema 迁移。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	testprocess "github.com/RecRivenVI/gallery/tools/testlab/internal/process"
)

const (
	beforeBackupLibrary   = "Portable upgrade persistent fact"
	afterBackupLibrary    = "Portable upgrade restore sentinel"
	afterBadBackupLibrary = "Portable failed restore current fact"
	startupTimeout        = 60 * time.Second
	jobTimeout            = 30 * time.Second
)

type pairedClient struct {
	api    *api.ClientWithResponses
	csrf   string
	editor api.RequestEditorFn
}

type result struct {
	PreviousVersion          string `json:"previousVersion"`
	CurrentVersion           string `json:"currentVersion"`
	BackupAppVersion         string `json:"backupAppVersion"`
	BackupSchemaVersion      int64  `json:"backupSchemaVersion"`
	RestoreWillMigrate       bool   `json:"restoreWillMigrate"`
	ProgramDataSeparated     bool   `json:"programDataSeparated"`
	FactsSurvivedTransition  bool   `json:"factsSurvivedTransition"`
	BackupVerified           bool   `json:"backupVerified"`
	RestoreAppliedOnRestart  bool   `json:"restoreAppliedOnRestart"`
	FailedRestoreKeptCurrent bool   `json:"failedRestoreKeptCurrent"`
	FailedRestoreRecorded    bool   `json:"failedRestoreRecorded"`
	AllStopsExitedGracefully bool   `json:"allStopsExitedGracefully"`
}

type restoreLastRecord struct {
	BackupID string `json:"backupId"`
	Applied  bool   `json:"applied"`
	Detail   string `json:"detail"`
}

func main() {
	previousBin := flag.String("previous-bin", "", "上一产品版本的 galleryd.exe")
	currentBin := flag.String("current-bin", "", "当前产品版本的 galleryd.exe")
	previousVersion := flag.String("previous-version", "", "上一二进制必须报告的产品版本")
	currentVersion := flag.String("current-version", "", "当前二进制必须报告的产品版本")
	flag.Parse()

	if err := run(*previousBin, *currentBin, *previousVersion, *currentVersion); err != nil {
		fmt.Fprintf(os.Stderr, "portable upgrade smoke 失败：%v\n", err)
		os.Exit(1)
	}
}

func run(previousBin, currentBin, previousVersion, currentVersion string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("portable upgrade smoke 只能在 Windows 上执行")
	}
	if previousBin == "" || currentBin == "" || previousVersion == "" || currentVersion == "" {
		return fmt.Errorf("必须完整指定两个二进制及其产品版本")
	}
	if previousVersion == currentVersion {
		return fmt.Errorf("上一版本与当前版本必须不同")
	}
	previousPath, err := filepath.Abs(previousBin)
	if err != nil {
		return fmt.Errorf("解析上一二进制: %w", err)
	}
	currentPath, err := filepath.Abs(currentBin)
	if err != nil {
		return fmt.Errorf("解析当前二进制: %w", err)
	}
	if strings.EqualFold(previousPath, currentPath) {
		return fmt.Errorf("两个版本不能复用同一个二进制文件")
	}
	if err := assertVersion(previousPath, previousVersion); err != nil {
		return err
	}
	if err := assertVersion(currentPath, currentVersion); err != nil {
		return err
	}

	testRoot, err := os.MkdirTemp("", "gallery-portable-upgrade-")
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
		ctx, previousPath, appRoot, filepath.Join(logs, "previous.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("启动上一版本: %w", err)
	}
	previousClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("配对上一版本: %w", err)
	}
	if err := createLibrary(ctx, previousClient, beforeBackupLibrary); err != nil {
		return err
	}
	backup, err := createBackup(ctx, previousClient)
	if err != nil {
		return err
	}
	if backup.AppVersion != previousVersion {
		return fmt.Errorf("备份记录的产品版本不匹配：got=%q want=%q", backup.AppVersion, previousVersion)
	}
	if err := createLibrary(ctx, previousClient, afterBackupLibrary); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("上一版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-before-restore.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("以同一 AppDirs 启动当前版本: %w", err)
	}
	currentClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("配对当前版本: %w", err)
	}
	if err := assertLibraries(ctx, currentClient, map[string]bool{
		beforeBackupLibrary: true,
		afterBackupLibrary:  true,
	}); err != nil {
		return fmt.Errorf("版本切换后的用户事实: %w", err)
	}
	verify, err := verifyBackup(ctx, currentClient, backup.BackupId)
	if err != nil {
		return err
	}
	if !verify.Compatible || !verify.ChecksumVerified || !verify.IntegrityOk || !verify.InvariantsOk {
		return fmt.Errorf("control 备份 dry-run 未通过全部检查")
	}
	if err := requestRestore(ctx, currentClient, backup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-restore.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("应用待重启恢复: %w", err)
	}
	restoredClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("恢复后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, restoredClient, map[string]bool{
		beforeBackupLibrary: true,
		afterBackupLibrary:  false,
	}); err != nil {
		return fmt.Errorf("恢复后的用户事实: %w", err)
	}

	badBackup, err := createBackup(ctx, restoredClient)
	if err != nil {
		return fmt.Errorf("创建失败回滚夹具备份: %w", err)
	}
	if err := createLibrary(ctx, restoredClient, afterBadBackupLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, restoredClient, badBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("损坏备份恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil
	if err := corruptBackup(appRoot, badBackup.BackupId); err != nil {
		return err
	}

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-failed-restore.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("损坏备份后启动当前版本: %w", err)
	}
	failedRestoreClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("损坏备份后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, failedRestoreClient, map[string]bool{
		beforeBackupLibrary:   true,
		afterBackupLibrary:    false,
		afterBadBackupLibrary: true,
	}); err != nil {
		return fmt.Errorf("损坏备份后的当前用户事实: %w", err)
	}
	if err := assertFailedRestoreRecorded(appRoot, badBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("损坏备份回滚后当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	value := result{
		PreviousVersion:          previousVersion,
		CurrentVersion:           currentVersion,
		BackupAppVersion:         backup.AppVersion,
		BackupSchemaVersion:      backup.SchemaVersion,
		RestoreWillMigrate:       verify.WillMigrate,
		ProgramDataSeparated:     true,
		FactsSurvivedTransition:  true,
		BackupVerified:           true,
		RestoreAppliedOnRestart:  true,
		FailedRestoreKeptCurrent: true,
		FailedRestoreRecorded:    true,
		AllStopsExitedGracefully: stopsGraceful,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func assertVersion(binary, expected string) error {
	output, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("执行二进制版本命令: %w", err)
	}
	if got := strings.TrimSpace(string(output)); got != "galleryd "+expected {
		return fmt.Errorf("二进制产品版本不匹配：got=%q want=%q", got, "galleryd "+expected)
	}
	return nil
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
		return api.ControlRestoreReport{}, fmt.Errorf("验证 control 备份：status=%d err=%v", statusCode(response), err)
	}
	return *response.JSON200, nil
}

func requestRestore(ctx context.Context, client *pairedClient, backupID string) error {
	response, err := client.api.RequestControlRestoreWithResponse(
		ctx,
		&api.RequestControlRestoreParams{XGalleryCSRF: client.csrf},
		api.ControlRestoreRequest{BackupId: backupID},
		client.editor,
	)
	if err != nil || response.JSON202 == nil {
		return fmt.Errorf("登记 control 恢复失败：status=%d err=%v", statusCode(response), err)
	}
	if !response.JSON202.RestartRequired || response.JSON202.Report.BackupId != backupID {
		return fmt.Errorf("恢复登记没有返回精确待重启事实")
	}
	return nil
}

func corruptBackup(appRoot, backupID string) error {
	if backupID == "" || filepath.Base(backupID) != backupID || strings.ContainsAny(backupID, "/\\:") {
		return fmt.Errorf("拒绝损坏非法备份身份")
	}
	target := filepath.Join(appRoot, "state", "backups", backupID, "control.db")
	relative, err := filepath.Rel(appRoot, target)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("拒绝损坏测试根之外的备份")
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("定位失败回滚夹具备份: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("失败回滚夹具备份不是普通文件")
	}
	if err := os.WriteFile(target, []byte("intentional corrupt backup fixture"), 0o600); err != nil {
		return fmt.Errorf("损坏失败回滚夹具备份: %w", err)
	}
	return nil
}

func assertFailedRestoreRecorded(appRoot, backupID string) error {
	pendingPath := filepath.Join(appRoot, "state", "restore-pending.json")
	if _, err := os.Stat(pendingPath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("损坏备份恢复标记未被消费")
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "state", "restore-last.json"))
	if err != nil {
		return fmt.Errorf("读取损坏备份恢复结果: %w", err)
	}
	var record restoreLastRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("解析损坏备份恢复结果: %w", err)
	}
	if record.BackupID != backupID || record.Applied || strings.TrimSpace(record.Detail) == "" {
		return fmt.Errorf("损坏备份恢复结果未精确记录")
	}
	return nil
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
	return validateLibraryPresence(response.JSON200.Libraries, expected)
}

func validateLibraryPresence(libraries []api.Library, expected map[string]bool) error {
	actual := make(map[string]bool, len(libraries))
	for _, library := range libraries {
		actual[library.Name] = true
	}
	for name, want := range expected {
		if actual[name] != want {
			return fmt.Errorf("Library %q 存在性不匹配：got=%t want=%t", name, actual[name], want)
		}
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
