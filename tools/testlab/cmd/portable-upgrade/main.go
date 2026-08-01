// Command portable-upgrade 使用两个正式 Windows 便携包中的 galleryd 二进制，
// 验证程序/数据分离、跨产品版本标签重启、control 备份验证与待重启恢复。
// 两个二进制可以来自同一源码提交；这种模式只证明制品编排，不冒充真实历史 Schema 迁移。
package main

import (
	"context"
	"crypto/sha256"
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
	"strings"
	"time"

	api "github.com/RecRivenVI/gallery/api"
	testprocess "github.com/RecRivenVI/gallery/tools/testlab/internal/process"
)

const (
	beforeBackupLibrary    = "Portable upgrade persistent fact"
	afterBackupLibrary     = "Portable upgrade restore sentinel"
	afterBadBackupLibrary  = "Portable failed restore current fact"
	afterLockedLibrary     = "Portable locked restore current fact"
	afterACLLibrary        = "Portable ACL restore current fact"
	afterLandingLibrary    = "Portable landing restore current fact"
	afterContinuityLibrary = "Portable continuity restore current fact"
	afterFinalizeLibrary   = "Portable finalize resume current fact"
	afterOutcomeLibrary    = "Portable outcome write current fact"
	afterPendingLibrary    = "Portable pending delete current fact"
	afterDoubleLibrary     = "Portable double rename current fact"
	afterKillLibrary       = "Portable finalize window kill current fact"
	startupTimeout         = 60 * time.Second
	jobTimeout             = 30 * time.Second
)

type pairedClient struct {
	api        *api.ClientWithResponses
	httpClient *http.Client
	csrf       string
	editor     api.RequestEditorFn
}

type result struct {
	PreviousVersion           string `json:"previousVersion"`
	CurrentVersion            string `json:"currentVersion"`
	BackupAppVersion          string `json:"backupAppVersion"`
	BackupSchemaVersion       int64  `json:"backupSchemaVersion"`
	RestoreWillMigrate        bool   `json:"restoreWillMigrate"`
	ProgramDataSeparated      bool   `json:"programDataSeparated"`
	FactsSurvivedTransition   bool   `json:"factsSurvivedTransition"`
	BackupVerified            bool   `json:"backupVerified"`
	RestoreAppliedOnRestart   bool   `json:"restoreAppliedOnRestart"`
	FailedRestoreKeptCurrent  bool   `json:"failedRestoreKeptCurrent"`
	FailedRestoreRecorded     bool   `json:"failedRestoreRecorded"`
	LockedRestoreKeptCurrent  bool   `json:"lockedRestoreKeptCurrent"`
	LockedRestoreRecorded     bool   `json:"lockedRestoreRecorded"`
	ACLRestoreKeptCurrent     bool   `json:"aclRestoreKeptCurrent"`
	ACLRestoreRecorded        bool   `json:"aclRestoreRecorded"`
	ACLRestoreBlockedByOS     bool   `json:"aclRestoreBlockedByOS"`
	LandingRestoreKeptCurrent bool   `json:"landingRestoreKeptCurrent"`
	LandingRestoreRecorded    bool   `json:"landingRestoreRecorded"`
	LandingRestoreBlockedByOS bool   `json:"landingRestoreBlockedByOS"`
	ContinuityFailedClosed    bool   `json:"continuityFailedClosed"`
	ContinuityPendingRetained bool   `json:"continuityPendingRetained"`
	ContinuityRecovered       bool   `json:"continuityRecovered"`
	ContinuityBlockedByOS     bool   `json:"continuityBlockedByOS"`
	FinalizeResumeKeptCurrent bool   `json:"finalizeResumeKeptCurrent"`
	FinalizeResumeRevokedAuth bool   `json:"finalizeResumeRevokedAuth"`
	FinalizeResumeCompleted   bool   `json:"finalizeResumeCompleted"`
	OutcomeWriteFailedClosed  bool   `json:"outcomeWriteFailedClosed"`
	OutcomeWriteRetained      bool   `json:"outcomeWriteRetained"`
	OutcomeWriteRecovered     bool   `json:"outcomeWriteRecovered"`
	PendingDeleteFailedClosed bool   `json:"pendingDeleteFailedClosed"`
	PendingDeleteRetained     bool   `json:"pendingDeleteRetained"`
	PendingDeleteRecovered    bool   `json:"pendingDeleteRecovered"`
	PendingDeleteBlockedByOS  bool   `json:"pendingDeleteBlockedByOS"`
	DoubleRenameFailedClosed  bool   `json:"doubleRenameFailedClosed"`
	DoubleRenameRetained      bool   `json:"doubleRenameRetained"`
	DoubleRenameRecovered     bool   `json:"doubleRenameRecovered"`
	DoubleRenameBlockedByOS   bool   `json:"doubleRenameBlockedByOS"`
	FinalizeWindowForcedKill  bool   `json:"finalizeWindowForcedKill"`
	FinalizeWindowRetained    bool   `json:"finalizeWindowRetained"`
	FinalizeWindowRecovered   bool   `json:"finalizeWindowRecovered"`
	AllStopsExitedGracefully  bool   `json:"allStopsExitedGracefully"`
}

type pendingFileHold struct {
	path    string
	release func() error
	err     error
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
	if err := requireSupportedPlatform(); err != nil {
		return err
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
	if err := assertFailedRestoreRecorded(appRoot, badBackup.BackupId, ""); err != nil {
		return err
	}

	lockedBackup, err := createBackup(ctx, failedRestoreClient)
	if err != nil {
		return fmt.Errorf("创建轮换拒绝夹具备份: %w", err)
	}
	if err := createLibrary(ctx, failedRestoreClient, afterLockedLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, failedRestoreClient, lockedBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("轮换拒绝恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil
	releaseLock, err := holdControlWithoutDeleteSharing(filepath.Join(appRoot, "data", "control.db"))
	if err != nil {
		return err
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-locked-restore.log"), startupTimeout,
	)
	releaseErr := releaseLock()
	if err != nil {
		return fmt.Errorf("轮换被拒绝后启动当前版本: %w", err)
	}
	if releaseErr != nil {
		return fmt.Errorf("释放 control 轮换阻断句柄: %w", releaseErr)
	}
	lockedRestoreClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("轮换被拒绝后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, lockedRestoreClient, map[string]bool{
		beforeBackupLibrary:   true,
		afterBackupLibrary:    false,
		afterBadBackupLibrary: true,
		afterLockedLibrary:    true,
	}); err != nil {
		return fmt.Errorf("轮换被拒绝后的当前用户事实: %w", err)
	}
	if err := assertFailedRestoreRecorded(appRoot, lockedBackup.BackupId, "轮换当前 control.db"); err != nil {
		return err
	}
	aclBackup, err := createBackup(ctx, lockedRestoreClient)
	if err != nil {
		return fmt.Errorf("创建 ACL 轮换拒绝夹具备份: %w", err)
	}
	if err := createLibrary(ctx, lockedRestoreClient, afterACLLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, lockedRestoreClient, aclBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("ACL 轮换拒绝恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil
	controlPath := filepath.Join(appRoot, "data", "control.db")
	restoreACL, err := denyCurrentUserDeleteWithACL(controlPath)
	if err != nil {
		return err
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-acl-restore.log"), startupTimeout,
	)
	aclRestoreErr := restoreACL()
	if err != nil {
		return errors.Join(fmt.Errorf("ACL 轮换被拒绝后启动当前版本: %w", err), aclRestoreErr)
	}
	if aclRestoreErr != nil {
		return fmt.Errorf("恢复 control.db ACL: %w", aclRestoreErr)
	}
	aclRestoreClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("ACL 轮换被拒绝后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, aclRestoreClient, map[string]bool{
		beforeBackupLibrary:   true,
		afterBackupLibrary:    false,
		afterBadBackupLibrary: true,
		afterLockedLibrary:    true,
		afterACLLibrary:       true,
	}); err != nil {
		return fmt.Errorf("ACL 轮换被拒绝后的当前用户事实: %w", err)
	}
	if err := assertFailedRestoreRecorded(appRoot, aclBackup.BackupId, "轮换当前 control.db"); err != nil {
		return err
	}
	if err := assertFailedRestoreRecorded(appRoot, aclBackup.BackupId, accessDeniedMessage()); err != nil {
		return fmt.Errorf("ACL 轮换失败未记录操作系统 access denied: %w", err)
	}

	landingBackup, err := createBackup(ctx, aclRestoreClient)
	if err != nil {
		return fmt.Errorf("创建候选落位拒绝夹具备份: %w", err)
	}
	if err := createLibrary(ctx, aclRestoreClient, afterLandingLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, aclRestoreClient, landingBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("候选落位拒绝恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	watchCtx, cancelWatch := context.WithCancel(ctx)
	holdResults, stopWatcher, err := watchNextFileWithoutDeleteSharing(
		watchCtx,
		filepath.Join(appRoot, "data", "control.db.incoming"),
	)
	if err != nil {
		cancelWatch()
		return err
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-landing-restore.log"), startupTimeout,
	)
	if err != nil {
		cancelWatch()
		_ = stopWatcher()
		return fmt.Errorf("候选落位被拒绝后启动当前版本: %w", err)
	}
	var hold pendingFileHold
	select {
	case hold = <-holdResults:
	case <-time.After(2 * time.Second):
		cancelWatch()
		_ = stopWatcher()
		return fmt.Errorf("未观察到恢复候选的真实 Windows 阻断句柄")
	}
	cancelWatch()
	watchStopErr := stopWatcher()
	if hold.err != nil {
		return fmt.Errorf("建立恢复候选落位阻断句柄: %w", hold.err)
	}
	if hold.release == nil {
		return fmt.Errorf("恢复候选落位阻断未返回可释放句柄")
	}
	landingReleaseErr := hold.release()
	if watchStopErr != nil {
		return fmt.Errorf("停止恢复候选目录监视: %w", watchStopErr)
	}
	if landingReleaseErr != nil {
		return fmt.Errorf("释放恢复候选落位阻断句柄: %w", landingReleaseErr)
	}
	landingRestoreClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("候选落位被拒绝后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, landingRestoreClient, map[string]bool{
		beforeBackupLibrary:   true,
		afterBackupLibrary:    false,
		afterBadBackupLibrary: true,
		afterLockedLibrary:    true,
		afterACLLibrary:       true,
		afterLandingLibrary:   true,
	}); err != nil {
		return fmt.Errorf("候选落位被拒绝后的当前用户事实: %w", err)
	}
	if err := assertFailedRestoreRecorded(appRoot, landingBackup.BackupId, "落位恢复候选"); err != nil {
		return err
	}
	continuityBackup, err := createBackup(ctx, landingRestoreClient)
	if err != nil {
		return fmt.Errorf("创建连续性失败夹具备份: %w", err)
	}
	if err := createLibrary(ctx, landingRestoreClient, afterContinuityLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, landingRestoreClient, continuityBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("连续性失败恢复前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	controlPath = filepath.Join(appRoot, "data", "control.db")
	preservedControl := filepath.Join(appRoot, "data", "control.db.continuity-current")
	if err := os.Rename(controlPath, preservedControl); err != nil {
		return fmt.Errorf("保全连续性失败前当前库: %w", err)
	}
	continuityIncoming := controlPath + ".incoming"
	if err := os.WriteFile(continuityIncoming, []byte("blocked continuity candidate fixture"), 0o600); err != nil {
		return fmt.Errorf("建立连续性失败候选夹具: %w", err)
	}
	releaseContinuityHold, err := holdControlWithoutDeleteSharing(continuityIncoming)
	if err != nil {
		return fmt.Errorf("持有连续性失败候选夹具: %w", err)
	}
	if removeErr := os.Remove(continuityIncoming); removeErr == nil {
		_ = releaseContinuityHold()
		return fmt.Errorf("真实 Windows handle 未阻止连续性候选删除")
	}
	continuityLog := filepath.Join(logs, "current-continuity-fail-closed.log")
	unexpected, startErr := testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, continuityLog, startupTimeout,
	)
	if unexpected != nil {
		outcome := unexpected.Stop()
		_ = releaseContinuityHold()
		return fmt.Errorf(
			"连续性未知时 galleryd 意外发布 descriptor：forced=%t err=%v",
			outcome.ForcedKill,
			outcome.Err,
		)
	}
	if continuityReleaseErr := releaseContinuityHold(); continuityReleaseErr != nil {
		return fmt.Errorf("释放连续性失败候选阻断句柄: %w", continuityReleaseErr)
	}
	if startErr == nil || !strings.Contains(startErr.Error(), "descriptor 前提前退出") {
		return fmt.Errorf("连续性未知时未在 descriptor 前 fail-closed: %v", startErr)
	}
	continuityLogData, err := os.ReadFile(continuityLog)
	if err != nil {
		return fmt.Errorf("读取连续性失败进程日志: %w", err)
	}
	if !strings.Contains(string(continuityLogData), "RESTORE_FAILED:") {
		return fmt.Errorf("连续性未知进程日志未记录 RESTORE_FAILED")
	}
	if err := assertContinuityRestoreRecorded(appRoot, continuityBackup.BackupId, "当前 control.db 不存在"); err != nil {
		return err
	}
	if _, err := os.Stat(controlPath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("连续性未知失败后 control.db 路径不应被重新创建")
	}
	if err := os.Rename(preservedControl, controlPath); err != nil {
		return fmt.Errorf("解除阻断后恢复已保全当前库: %w", err)
	}

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-continuity-recovery.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("解除连续性阻断后重试恢复: %w", err)
	}
	continuityClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("连续性恢复后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, continuityClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
	}); err != nil {
		return fmt.Errorf("解除连续性阻断后的恢复事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, continuityBackup.BackupId); err != nil {
		return err
	}
	if err := createLibrary(ctx, continuityClient, afterFinalizeLibrary); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("finalize 续接前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil
	// 构造“control.db 已落位，但进程在恢复后安全收尾前中断”留下的持久阶段。这里不改数据库、
	// 不复制备份，只登记生产状态机实际读取的阶段；随后必须由未打测试 tag 的便携 galleryd
	// 在同一 AppDirs 中完成 FinalizeRestore，且不能再次应用备份覆盖刚创建的当前事实。
	if err := writeFinalizeResumeMarker(appRoot, continuityBackup.BackupId); err != nil {
		return err
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-finalize-resume.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("启动待 finalize 恢复阶段: %w", err)
	}
	staleClient, err := rebindPairedClient(active.BaseURL, continuityClient)
	if err != nil {
		return fmt.Errorf("重绑定恢复前 Session: %w", err)
	}
	if err := assertSessionInvalidated(ctx, staleClient); err != nil {
		return err
	}
	finalizeClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("finalize 续接后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, finalizeClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
		afterFinalizeLibrary:   true,
	}); err != nil {
		return fmt.Errorf("finalize 续接后的当前事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, continuityBackup.BackupId); err != nil {
		return fmt.Errorf("finalize 续接结果: %w", err)
	}
	if err := createLibrary(ctx, finalizeClient, afterOutcomeLibrary); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("结果写入失败前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	if err := writeFinalizeResumeMarker(appRoot, continuityBackup.BackupId); err != nil {
		return err
	}
	releaseOutcomeBlock, err := blockRestoreOutcomePath(appRoot)
	if err != nil {
		return err
	}
	outcomeLog := filepath.Join(logs, "current-outcome-write-fail-closed.log")
	if err := assertRestoreStartupFailed(ctx, currentPath, appRoot, outcomeLog, "记录恢复结果"); err != nil {
		_ = releaseOutcomeBlock()
		return err
	}
	if err := assertPendingFinalizeMarker(appRoot, continuityBackup.BackupId); err != nil {
		_ = releaseOutcomeBlock()
		return err
	}
	if err := releaseOutcomeBlock(); err != nil {
		return fmt.Errorf("解除恢复结果写入阻断: %w", err)
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-outcome-write-recovery.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("恢复结果写入解除阻断后启动: %w", err)
	}
	outcomeClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("恢复结果写入解除阻断后配对: %w", err)
	}
	if err := assertLibraries(ctx, outcomeClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
		afterFinalizeLibrary:   true,
		afterOutcomeLibrary:    true,
	}); err != nil {
		return fmt.Errorf("恢复结果写入解除阻断后的当前事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, continuityBackup.BackupId); err != nil {
		return fmt.Errorf("恢复结果写入解除阻断后的结果: %w", err)
	}
	if err := createLibrary(ctx, outcomeClient, afterPendingLibrary); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("pending 删除失败前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	if err := writeFinalizeResumeMarker(appRoot, continuityBackup.BackupId); err != nil {
		return err
	}
	pendingPath := filepath.Join(appRoot, "state", "restore-pending.json")
	releasePendingHold, err := holdControlWithoutDeleteSharing(pendingPath)
	if err != nil {
		return fmt.Errorf("持有恢复 pending 阻断句柄: %w", err)
	}
	if removeErr := os.Remove(pendingPath); removeErr == nil {
		_ = releasePendingHold()
		return fmt.Errorf("真实 Windows handle 未阻止恢复 pending 删除")
	} else if !isDeleteSharingViolation(removeErr) {
		_ = releasePendingHold()
		return fmt.Errorf("恢复 pending 删除未返回 Windows sharing violation: %w", removeErr)
	}
	pendingLog := filepath.Join(logs, "current-pending-delete-fail-closed.log")
	if err := assertRestoreStartupFailed(ctx, currentPath, appRoot, pendingLog, "消费恢复请求"); err != nil {
		_ = releasePendingHold()
		return err
	}
	if err := assertPendingFinalizeMarker(appRoot, continuityBackup.BackupId); err != nil {
		_ = releasePendingHold()
		return err
	}
	if err := releasePendingHold(); err != nil {
		return fmt.Errorf("释放恢复 pending 阻断句柄: %w", err)
	}
	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-pending-delete-recovery.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("恢复 pending 删除解除阻断后启动: %w", err)
	}
	pendingClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("恢复 pending 删除解除阻断后配对: %w", err)
	}
	if err := assertLibraries(ctx, pendingClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
		afterFinalizeLibrary:   true,
		afterOutcomeLibrary:    true,
		afterPendingLibrary:    true,
	}); err != nil {
		return fmt.Errorf("恢复 pending 删除解除阻断后的当前事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, continuityBackup.BackupId); err != nil {
		return fmt.Errorf("恢复 pending 删除解除阻断后的结果: %w", err)
	}
	doubleRenameBackup, err := createBackup(ctx, pendingClient)
	if err != nil {
		return fmt.Errorf("创建双 Rename 失败夹具备份: %w", err)
	}
	if err := createLibrary(ctx, pendingClient, afterDoubleLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, pendingClient, doubleRenameBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("双 Rename 失败前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	controlPath = filepath.Join(appRoot, "data", "control.db")
	currentDigest, err := digestFile(controlPath)
	if err != nil {
		return fmt.Errorf("封印双 Rename 失败前当前库: %w", err)
	}
	rotatedPattern := filepath.Join(appRoot, "data", "control.db.pre-restore-*.bak")
	rotatedBefore, err := filepath.Glob(rotatedPattern)
	if err != nil {
		return fmt.Errorf("枚举双 Rename 失败前轮换副本: %w", err)
	}
	doubleCtx, cancelDouble := context.WithCancel(ctx)
	incomingResults, stopIncomingWatcher, err := watchNextFileWithoutDeleteSharing(
		doubleCtx,
		controlPath+".incoming",
	)
	if err != nil {
		cancelDouble()
		return err
	}
	rollbackResults, stopRollbackWatcher, err := watchPathMissingThenReopenWithoutDeleteSharing(
		doubleCtx,
		controlPath,
	)
	if err != nil {
		cancelDouble()
		_ = stopIncomingWatcher()
		releaseReadyPendingFileHold(incomingResults)
		return err
	}
	doubleLog := filepath.Join(logs, "current-double-rename-fail-closed.log")
	unexpected, startErr = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, doubleLog, startupTimeout,
	)
	if unexpected != nil {
		outcome := unexpected.Stop()
		cancelDouble()
		_ = stopIncomingWatcher()
		_ = stopRollbackWatcher()
		releaseReadyPendingFileHold(incomingResults)
		releaseReadyPendingFileHold(rollbackResults)
		return fmt.Errorf(
			"双 Rename 失败时 galleryd 意外发布 descriptor：forced=%t err=%v",
			outcome.ForcedKill,
			outcome.Err,
		)
	}
	incomingHold, err := awaitPendingFileHold(incomingResults, "恢复候选落位")
	if err != nil {
		cancelDouble()
		_ = stopIncomingWatcher()
		_ = stopRollbackWatcher()
		releaseReadyPendingFileHold(incomingResults)
		releaseReadyPendingFileHold(rollbackResults)
		return err
	}
	rollbackHold, err := awaitPendingFileHold(rollbackResults, "旧库回滚")
	if err != nil {
		_ = incomingHold.release()
		cancelDouble()
		_ = stopIncomingWatcher()
		_ = stopRollbackWatcher()
		releaseReadyPendingFileHold(rollbackResults)
		return err
	}
	cancelDouble()
	incomingWatchErr := stopIncomingWatcher()
	rollbackWatchErr := stopRollbackWatcher()
	if startErr == nil || !strings.Contains(startErr.Error(), "descriptor 前提前退出") {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("双 Rename 失败时未在 descriptor 前 fail-closed: %v", startErr)
	}
	if incomingWatchErr != nil || rollbackWatchErr != nil {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("停止双 Rename 阻断监视: incoming=%v rollback=%v", incomingWatchErr, rollbackWatchErr)
	}
	doubleLogData, err := os.ReadFile(doubleLog)
	if err != nil {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("读取双 Rename 失败进程日志: %w", err)
	}
	if !strings.Contains(string(doubleLogData), "RESTORE_FAILED:") ||
		!strings.Contains(string(doubleLogData), "落位恢复候选") ||
		!strings.Contains(string(doubleLogData), "回滚当前 control.db") {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("双 Rename 失败进程日志未记录两个精确失败阶段")
	}
	if err := assertContinuityRestoreRecorded(appRoot, doubleRenameBackup.BackupId, "落位恢复候选"); err != nil {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return err
	}
	if err := assertContinuityRestoreRecorded(appRoot, doubleRenameBackup.BackupId, "回滚当前 control.db"); err != nil {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return err
	}
	if _, err := os.Stat(controlPath); !errors.Is(err, os.ErrNotExist) {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("双 Rename 失败后 control.db 路径不应被重新创建")
	}
	rotatedPath, err := findOnlyNewPath(rotatedBefore, rotatedPattern)
	if err != nil {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return err
	}
	rotatedDigest, err := digestFile(rotatedPath)
	if err != nil || rotatedDigest != currentDigest {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("双 Rename 失败后的旧库轮换副本未保持精确字节: %v", err)
	}
	if renameErr := os.Rename(incomingHold.path, controlPath); renameErr == nil || !isDeleteSharingViolation(renameErr) {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("候选落位未保持 Windows sharing violation: %v", renameErr)
	}
	if renameErr := os.Rename(rotatedPath, controlPath); renameErr == nil || !isDeleteSharingViolation(renameErr) {
		_ = incomingHold.release()
		_ = rollbackHold.release()
		return fmt.Errorf("旧库回滚未保持 Windows sharing violation: %v", renameErr)
	}
	if err := incomingHold.release(); err != nil {
		_ = rollbackHold.release()
		return fmt.Errorf("释放候选落位阻断句柄: %w", err)
	}
	if err := rollbackHold.release(); err != nil {
		return fmt.Errorf("释放旧库回滚阻断句柄: %w", err)
	}

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-double-rename-recovery.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("解除双 Rename 阻断后重试恢复: %w", err)
	}
	doubleClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("双 Rename 恢复后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, doubleClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
		afterFinalizeLibrary:   true,
		afterOutcomeLibrary:    true,
		afterPendingLibrary:    true,
		afterDoubleLibrary:     false,
	}); err != nil {
		return fmt.Errorf("双 Rename 解除阻断后的恢复事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, doubleRenameBackup.BackupId); err != nil {
		return fmt.Errorf("双 Rename 解除阻断后的恢复结果: %w", err)
	}
	retainedDigest, err := digestFile(rotatedPath)
	if err != nil || retainedDigest != currentDigest {
		return fmt.Errorf("双 Rename 恢复后轮换副本未保持当前事实字节: %v", err)
	}
	finalizeKillBackup, err := createBackup(ctx, doubleClient)
	if err != nil {
		return fmt.Errorf("创建 finalize 窗口强杀夹具备份: %w", err)
	}
	if err := createLibrary(ctx, doubleClient, afterKillLibrary); err != nil {
		return err
	}
	if err := requestRestore(ctx, doubleClient, finalizeKillBackup.BackupId); err != nil {
		return err
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("finalize 窗口强杀前当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	finalizeCurrentDigest, err := digestFile(controlPath)
	if err != nil {
		return fmt.Errorf("封印 finalize 窗口强杀前当前库: %w", err)
	}
	finalizeRotatedBefore, err := filepath.Glob(rotatedPattern)
	if err != nil {
		return fmt.Errorf("枚举 finalize 窗口强杀前轮换副本: %w", err)
	}
	killCtx, cancelKill := context.WithCancel(ctx)
	phaseObserved, err := watchPendingFinalizePhase(killCtx, appRoot, cancelKill)
	if err != nil {
		cancelKill()
		return err
	}
	killLog := filepath.Join(logs, "current-finalize-window-kill.log")
	unexpected, killErr := testprocess.StartGallerydWithSourceRootsContext(
		killCtx, currentPath, appRoot, killLog, startupTimeout,
	)
	if unexpected != nil {
		outcome := unexpected.Stop()
		cancelKill()
		return fmt.Errorf(
			"finalize 窗口强杀前 galleryd 意外发布 descriptor：forced=%t err=%v",
			outcome.ForcedKill,
			outcome.Err,
		)
	}
	select {
	case phaseErr := <-phaseObserved:
		if phaseErr != nil {
			cancelKill()
			return phaseErr
		}
	case <-time.After(2 * time.Second):
		cancelKill()
		return fmt.Errorf("未确认 finalize 窗口持久阶段: startup=%v", killErr)
	}
	cancelKill()
	var termination *testprocess.StartupTerminationError
	if !errors.As(killErr, &termination) || !termination.ForcedKill || !errors.Is(killErr, context.Canceled) {
		return fmt.Errorf("finalize 窗口未形成显式强杀启动结果: %v", killErr)
	}
	if err := assertPendingFinalizeMarker(appRoot, finalizeKillBackup.BackupId); err != nil {
		return fmt.Errorf("finalize 窗口强杀后 pending: %w", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "run", "galleryd.json")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("finalize 窗口强杀前意外发布 runtime descriptor")
	}
	finalizeRotatedPath, err := findOnlyNewPath(finalizeRotatedBefore, rotatedPattern)
	if err != nil {
		return fmt.Errorf("定位 finalize 窗口强杀轮换副本: %w", err)
	}
	finalizeRotatedDigest, err := digestFile(finalizeRotatedPath)
	if err != nil || finalizeRotatedDigest != finalizeCurrentDigest {
		return fmt.Errorf("finalize 窗口强杀后的轮换副本未保持当前事实字节: %v", err)
	}

	active, err = testprocess.StartGallerydWithSourceRootsContext(
		ctx, currentPath, appRoot, filepath.Join(logs, "current-after-finalize-window-kill.log"), startupTimeout,
	)
	if err != nil {
		return fmt.Errorf("finalize 窗口强杀后重启恢复: %w", err)
	}
	staleKillClient, err := rebindPairedClient(active.BaseURL, doubleClient)
	if err != nil {
		return fmt.Errorf("重绑定 finalize 窗口强杀前 Session: %w", err)
	}
	if err := assertSessionInvalidated(ctx, staleKillClient); err != nil {
		return err
	}
	killClient, err := pair(ctx, active.BaseURL)
	if err != nil {
		return fmt.Errorf("finalize 窗口强杀恢复后重新配对: %w", err)
	}
	if err := assertLibraries(ctx, killClient, map[string]bool{
		beforeBackupLibrary:    true,
		afterBackupLibrary:     false,
		afterBadBackupLibrary:  true,
		afterLockedLibrary:     true,
		afterACLLibrary:        true,
		afterLandingLibrary:    true,
		afterContinuityLibrary: false,
		afterFinalizeLibrary:   true,
		afterOutcomeLibrary:    true,
		afterPendingLibrary:    true,
		afterDoubleLibrary:     false,
		afterKillLibrary:       false,
	}); err != nil {
		return fmt.Errorf("finalize 窗口强杀恢复后的用户事实: %w", err)
	}
	if err := assertSuccessfulRestoreRecorded(appRoot, finalizeKillBackup.BackupId); err != nil {
		return fmt.Errorf("finalize 窗口强杀后的恢复结果: %w", err)
	}
	finalizeRetainedDigest, err := digestFile(finalizeRotatedPath)
	if err != nil || finalizeRetainedDigest != finalizeCurrentDigest {
		return fmt.Errorf("finalize 窗口恢复后的轮换副本未保持当前事实字节: %v", err)
	}
	if outcome := active.Stop(); !outcome.ExitedGracefully || outcome.ForcedKill || outcome.Err != nil {
		stopsGraceful = false
		return fmt.Errorf("finalize 窗口恢复后当前版本未优雅停止：forced=%t err=%v", outcome.ForcedKill, outcome.Err)
	}
	active = nil

	value := result{
		PreviousVersion:           previousVersion,
		CurrentVersion:            currentVersion,
		BackupAppVersion:          backup.AppVersion,
		BackupSchemaVersion:       backup.SchemaVersion,
		RestoreWillMigrate:        verify.WillMigrate,
		ProgramDataSeparated:      true,
		FactsSurvivedTransition:   true,
		BackupVerified:            true,
		RestoreAppliedOnRestart:   true,
		FailedRestoreKeptCurrent:  true,
		FailedRestoreRecorded:     true,
		LockedRestoreKeptCurrent:  true,
		LockedRestoreRecorded:     true,
		ACLRestoreKeptCurrent:     true,
		ACLRestoreRecorded:        true,
		ACLRestoreBlockedByOS:     true,
		LandingRestoreKeptCurrent: true,
		LandingRestoreRecorded:    true,
		LandingRestoreBlockedByOS: true,
		ContinuityFailedClosed:    true,
		ContinuityPendingRetained: true,
		ContinuityRecovered:       true,
		ContinuityBlockedByOS:     true,
		FinalizeResumeKeptCurrent: true,
		FinalizeResumeRevokedAuth: true,
		FinalizeResumeCompleted:   true,
		OutcomeWriteFailedClosed:  true,
		OutcomeWriteRetained:      true,
		OutcomeWriteRecovered:     true,
		PendingDeleteFailedClosed: true,
		PendingDeleteRetained:     true,
		PendingDeleteRecovered:    true,
		PendingDeleteBlockedByOS:  true,
		DoubleRenameFailedClosed:  true,
		DoubleRenameRetained:      true,
		DoubleRenameRecovered:     true,
		DoubleRenameBlockedByOS:   true,
		FinalizeWindowForcedKill:  true,
		FinalizeWindowRetained:    true,
		FinalizeWindowRecovered:   true,
		AllStopsExitedGracefully:  stopsGraceful,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func writeFinalizeResumeMarker(appRoot, backupID string) error {
	marker := struct {
		BackupID    string    `json:"backupId"`
		RequestedBy string    `json:"requestedBy"`
		RequestedAt time.Time `json:"requestedAt"`
		Phase       string    `json:"phase"`
	}{
		BackupID:    backupID,
		RequestedBy: "portable-upgrade-crash-fixture",
		RequestedAt: time.Now().UTC(),
		Phase:       "placed_pending_finalize",
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("编码待 finalize 恢复阶段: %w", err)
	}
	path := filepath.Join(appRoot, "state", "restore-pending.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入待 finalize 恢复阶段: %w", err)
	}
	return nil
}

func assertPendingFinalizeMarker(appRoot, backupID string) error {
	matches, err := pendingFinalizeMarkerMatches(appRoot, backupID)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("待 finalize 恢复阶段未精确保留")
	}
	return nil
}

func pendingFinalizeMarkerMatches(appRoot, backupID string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(appRoot, "state", "restore-pending.json"))
	if err != nil {
		return false, fmt.Errorf("读取待 finalize 恢复阶段: %w", err)
	}
	var marker struct {
		BackupID string `json:"backupId"`
		Phase    string `json:"phase"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return false, fmt.Errorf("解析待 finalize 恢复阶段: %w", err)
	}
	return marker.BackupID == backupID && marker.Phase == "placed_pending_finalize", nil
}

func watchPendingFinalizePhase(
	ctx context.Context,
	appRoot string,
	onObserved context.CancelFunc,
) (<-chan error, error) {
	path := filepath.Join(appRoot, "state", "restore-pending.json")
	observed, err := watchObservedFileReplacement(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("建立 finalize 状态替换观察: %w", err)
	}
	result := make(chan error, 1)
	go func() {
		err := <-observed
		if err != nil {
			result <- fmt.Errorf("等待 finalize 持久阶段: %w", err)
			return
		}
		onObserved()
		result <- nil
	}()
	return result, nil
}

func blockRestoreOutcomePath(appRoot string) (func() error, error) {
	path := filepath.Join(appRoot, "state", "restore-last.json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("移除旧恢复结果: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("建立恢复结果目录阻断: %w", err)
	}
	child := filepath.Join(path, "block")
	if err := os.WriteFile(child, []byte("block restore outcome replacement"), 0o600); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("建立恢复结果非空目录阻断: %w", err)
	}
	release := func() error {
		if err := os.Remove(child); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return release, nil
}

func assertRestoreStartupFailed(ctx context.Context, binary, appRoot, logPath, requiredDetail string) error {
	unexpected, startErr := testprocess.StartGallerydWithSourceRootsContext(
		ctx, binary, appRoot, logPath, startupTimeout,
	)
	if unexpected != nil {
		outcome := unexpected.Stop()
		return fmt.Errorf(
			"恢复状态失败时 galleryd 意外发布 descriptor：forced=%t err=%v",
			outcome.ForcedKill,
			outcome.Err,
		)
	}
	if startErr == nil || !strings.Contains(startErr.Error(), "descriptor 前提前退出") {
		return fmt.Errorf("恢复状态失败时未在 descriptor 前 fail-closed: %v", startErr)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("读取恢复状态失败进程日志: %w", err)
	}
	if !strings.Contains(string(data), "RESTORE_FAILED:") ||
		(requiredDetail != "" && !strings.Contains(string(data), requiredDetail)) {
		return fmt.Errorf("恢复状态失败进程日志未记录精确 RESTORE_FAILED 阶段")
	}
	return nil
}

func awaitPendingFileHold(results <-chan pendingFileHold, label string) (pendingFileHold, error) {
	select {
	case hold := <-results:
		if hold.err != nil {
			return pendingFileHold{}, fmt.Errorf("建立%s阻断句柄: %w", label, hold.err)
		}
		if hold.release == nil || hold.path == "" {
			return pendingFileHold{}, fmt.Errorf("%s阻断未返回完整句柄事实", label)
		}
		return hold, nil
	case <-time.After(2 * time.Second):
		return pendingFileHold{}, fmt.Errorf("未观察到%s的真实 Windows 阻断句柄", label)
	}
}

func releaseReadyPendingFileHold(results <-chan pendingFileHold) {
	select {
	case hold := <-results:
		if hold.release != nil {
			_ = hold.release()
		}
	default:
	}
}

func findOnlyNewPath(before []string, pattern string) (string, error) {
	known := make(map[string]struct{}, len(before))
	for _, path := range before {
		known[path] = struct{}{}
	}
	after, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("枚举轮换副本: %w", err)
	}
	var found []string
	for _, path := range after {
		if _, ok := known[path]; !ok {
			found = append(found, path)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("预期恰好一个新轮换副本，实际为 %d", len(found))
	}
	return found[0], nil
}

func digestFile(path string) ([sha256.Size]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func assertSessionInvalidated(ctx context.Context, client *pairedClient) error {
	response, err := client.api.ListLibrariesWithResponse(ctx, client.editor)
	if err != nil {
		return fmt.Errorf("验证恢复前 Session 失效: %w", err)
	}
	if statusCode(response) != http.StatusUnauthorized {
		return fmt.Errorf("恢复前 Session 未失效：status=%d", statusCode(response))
	}
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
	httpClient := &http.Client{Jar: jar}
	client, err := api.NewClientWithResponses(baseURL, api.WithHTTPClient(httpClient))
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
	return &pairedClient{api: client, httpClient: httpClient, csrf: exchange.JSON201.CsrfToken, editor: editor}, nil
}

func rebindPairedClient(baseURL string, existing *pairedClient) (*pairedClient, error) {
	if existing == nil || existing.httpClient == nil {
		return nil, fmt.Errorf("缺少可复用的恢复前 HTTP client")
	}
	client, err := api.NewClientWithResponses(baseURL, api.WithHTTPClient(existing.httpClient))
	if err != nil {
		return nil, err
	}
	editor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", baseURL)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
	return &pairedClient{
		api:        client,
		httpClient: existing.httpClient,
		csrf:       existing.csrf,
		editor:     editor,
	}, nil
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

func assertFailedRestoreRecorded(appRoot, backupID, requiredDetail string) error {
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
	if record.BackupID != backupID || record.Applied || strings.TrimSpace(record.Detail) == "" ||
		(requiredDetail != "" && !strings.Contains(record.Detail, requiredDetail)) {
		return fmt.Errorf("损坏备份恢复结果未精确记录")
	}
	return nil
}

func assertContinuityRestoreRecorded(appRoot, backupID, requiredDetail string) error {
	pendingData, err := os.ReadFile(filepath.Join(appRoot, "state", "restore-pending.json"))
	if err != nil {
		return fmt.Errorf("读取连续性失败恢复标记: %w", err)
	}
	var pending struct {
		BackupID string `json:"backupId"`
	}
	if err := json.Unmarshal(pendingData, &pending); err != nil || pending.BackupID != backupID {
		return fmt.Errorf("连续性失败恢复标记未精确保留")
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "state", "restore-last.json"))
	if err != nil {
		return fmt.Errorf("读取连续性失败恢复结果: %w", err)
	}
	var record restoreLastRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("解析连续性失败恢复结果: %w", err)
	}
	if record.BackupID != backupID || record.Applied || strings.TrimSpace(record.Detail) == "" ||
		(requiredDetail != "" && !strings.Contains(record.Detail, requiredDetail)) {
		return fmt.Errorf("连续性失败恢复结果未精确记录")
	}
	return nil
}

func assertSuccessfulRestoreRecorded(appRoot, backupID string) error {
	if _, err := os.Stat(filepath.Join(appRoot, "state", "restore-pending.json")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("连续性恢复成功后 pending 未消费")
	}
	data, err := os.ReadFile(filepath.Join(appRoot, "state", "restore-last.json"))
	if err != nil {
		return fmt.Errorf("读取连续性恢复成功结果: %w", err)
	}
	var record restoreLastRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("解析连续性恢复成功结果: %w", err)
	}
	if record.BackupID != backupID || !record.Applied || !strings.Contains(record.Detail, "已原子替换 control.db") {
		return fmt.Errorf("连续性恢复成功结果未精确记录")
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
