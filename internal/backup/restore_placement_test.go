package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
)

func writeRestoreFixture(t *testing.T, root string) (controlPath, incoming, rotated string) {
	t.Helper()
	controlPath = filepath.Join(root, "control.db")
	incoming = controlPath + incomingSuffix
	rotated = filepath.Join(root, preRestorePrefix+"test.bak")
	if err := os.WriteFile(controlPath, []byte("current-user-facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(incoming, []byte("restored-user-facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	return controlPath, incoming, rotated
}

func TestPlaceRestoreCandidateRollsBackLandingFailure(t *testing.T) {
	controlPath, incoming, rotated := writeRestoreFixture(t, t.TempDir())
	landingErr := errors.New("candidate landing blocked")
	ops := osRestoreFileOps
	ops.rename = func(oldPath, newPath string) error {
		if oldPath == incoming && newPath == controlPath {
			return landingErr
		}
		return os.Rename(oldPath, newPath)
	}

	_, err := placeRestoreCandidate(controlPath, incoming, rotated, ops)
	if !errors.Is(err, landingErr) {
		t.Fatalf("候选落位失败未保留根因: %v", err)
	}
	var continuityErr *restoreContinuityError
	if errors.As(err, &continuityErr) {
		t.Fatalf("旧库已成功回滚时不应升级为启动连续性错误: %v", err)
	}
	content, readErr := os.ReadFile(controlPath)
	if readErr != nil || string(content) != "current-user-facts" {
		t.Fatalf("候选落位失败后当前库未恢复: content=%q err=%v", content, readErr)
	}
	if _, statErr := os.Stat(rotated); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("成功回滚后仍残留轮换占位: %v", statErr)
	}
}

func TestPlaceRestoreCandidateFailsClosedWhenRollbackFails(t *testing.T) {
	controlPath, incoming, rotated := writeRestoreFixture(t, t.TempDir())
	landingErr := errors.New("candidate landing blocked")
	rollbackErr := errors.New("rollback blocked")
	ops := osRestoreFileOps
	ops.rename = func(oldPath, newPath string) error {
		switch {
		case oldPath == incoming && newPath == controlPath:
			return landingErr
		case oldPath == rotated && newPath == controlPath:
			return rollbackErr
		default:
			return os.Rename(oldPath, newPath)
		}
	}

	_, err := placeRestoreCandidate(controlPath, incoming, rotated, ops)
	var continuityErr *restoreContinuityError
	if !errors.As(err, &continuityErr) || !errors.Is(err, landingErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("落位与回滚双失败必须 fail-closed 并保留两项根因: %v", err)
	}
	if _, statErr := os.Stat(controlPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("测试夹具未形成 controlPath 缺失边界: %v", statErr)
	}
	content, readErr := os.ReadFile(rotated)
	if readErr != nil || string(content) != "current-user-facts" {
		t.Fatalf("回滚失败时轮换副本未保留用户事实: content=%q err=%v", content, readErr)
	}
	if !strings.Contains(err.Error(), "落位恢复候选") || !strings.Contains(err.Error(), "回滚当前 control.db") {
		t.Fatalf("失败阶段不可诊断: %v", err)
	}
}

func TestPlaceRestoreCandidateWithoutCurrentFailsClosedOnLandingFailure(t *testing.T) {
	root := t.TempDir()
	controlPath := filepath.Join(root, "control.db")
	incoming := controlPath + incomingSuffix
	rotated := filepath.Join(root, preRestorePrefix+"test.bak")
	if err := os.WriteFile(incoming, []byte("restored-user-facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	landingErr := errors.New("candidate landing blocked")
	ops := osRestoreFileOps
	ops.rename = func(oldPath, newPath string) error {
		if oldPath == incoming && newPath == controlPath {
			return landingErr
		}
		return os.Rename(oldPath, newPath)
	}

	_, err := placeRestoreCandidate(controlPath, incoming, rotated, ops)
	var continuityErr *restoreContinuityError
	if !errors.As(err, &continuityErr) || !errors.Is(err, landingErr) {
		t.Fatalf("无当前库且候选无法落位时必须阻止创建空库: %v", err)
	}
}

func TestPlaceRestoreCandidateRollsBackSidecarCleanupFailure(t *testing.T) {
	controlPath, incoming, rotated := writeRestoreFixture(t, t.TempDir())
	sidecarErr := errors.New("sidecar removal blocked")
	ops := osRestoreFileOps
	ops.remove = func(path string) error {
		if path == controlPath+"-wal" {
			return sidecarErr
		}
		return os.Remove(path)
	}

	_, err := placeRestoreCandidate(controlPath, incoming, rotated, ops)
	if !errors.Is(err, sidecarErr) {
		t.Fatalf("sidecar 清理失败未保留根因: %v", err)
	}
	var continuityErr *restoreContinuityError
	if errors.As(err, &continuityErr) {
		t.Fatalf("旧库已成功回滚时不应阻止后续启动: %v", err)
	}
	content, readErr := os.ReadFile(controlPath)
	if readErr != nil || string(content) != "current-user-facts" {
		t.Fatalf("sidecar 清理失败后当前库未恢复: content=%q err=%v", content, readErr)
	}
}

func TestHandleRestoreApplyFailureKeepsPendingWhenContinuityIsUnknown(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.Dirs{State: filepath.Join(root, "state")}
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dirs.State, restorePendingFile)
	if err := os.WriteFile(markerPath, []byte(`{"backupId":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cause := &restoreContinuityError{cause: errors.New("rollback unavailable")}
	err := handleRestoreApplyFailure(dirs, markerPath, "cbak_test", cause)
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeRestoreFailed {
		t.Fatalf("连续性未知时未返回 RESTORE_FAILED: %v", err)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("连续性未知时不应消费待恢复标记: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(dirs.State, restoreLastFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var recorded restoreLast
	if err := json.Unmarshal(data, &recorded); err != nil || recorded.Applied || !strings.Contains(recorded.Detail, "rollback unavailable") {
		t.Fatalf("失败事实未准确记录: %+v err=%v", recorded, err)
	}
}

func TestHandleRestoreApplyFailureConsumesPendingAfterSafeRollback(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.Dirs{Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	if err := os.MkdirAll(dirs.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.Data, databaseFileName), []byte("current-user-facts"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dirs.State, restorePendingFile)
	if err := os.WriteFile(markerPath, []byte(`{"backupId":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handleRestoreApplyFailure(dirs, markerPath, "cbak_test", errors.New("landing failed but rolled back")); err != nil {
		t.Fatalf("安全回滚后的普通恢复失败不应阻止启动: %v", err)
	}
	if _, statErr := os.Stat(markerPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("安全回滚后的失败标记未消费: %v", statErr)
	}
}

func TestHandleRestoreApplyFailureKeepsPendingWhenCurrentDatabaseIsMissing(t *testing.T) {
	root := t.TempDir()
	dirs := appdirs.Dirs{Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	if err := os.MkdirAll(dirs.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dirs.State, restorePendingFile)
	if err := os.WriteFile(markerPath, []byte(`{"backupId":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := handleRestoreApplyFailure(dirs, markerPath, "cbak_test", errors.New("候选生成前失败"))
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeRestoreFailed {
		t.Fatalf("当前库缺失时未返回 RESTORE_FAILED: %v", err)
	}
	if !strings.Contains(err.Error(), "当前 control.db 不存在") {
		t.Fatalf("当前库缺失原因不可诊断: %v", err)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("当前库缺失时不应消费待恢复标记: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(dirs.State, restoreLastFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var recorded restoreLast
	if unmarshalErr := json.Unmarshal(data, &recorded); unmarshalErr != nil ||
		!strings.Contains(recorded.Detail, "候选生成前失败") ||
		!strings.Contains(recorded.Detail, "当前 control.db 不存在") {
		t.Fatalf("当前库缺失失败事实未完整记录: %+v err=%v", recorded, unmarshalErr)
	}
}
