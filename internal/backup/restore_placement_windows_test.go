//go:build windows

package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"golang.org/x/sys/windows"
)

func openWithoutDeleteSharing(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return handle, nil
}

func TestPlaceRestoreCandidateFailsClosedOnRealWindowsDoubleSharingViolation(t *testing.T) {
	root := t.TempDir()
	controlPath, incoming, rotated := writeRestoreFixture(t, root)

	incomingHandle, err := openWithoutDeleteSharing(incoming)
	if err != nil {
		t.Fatal(err)
	}
	incomingOpen := true
	defer func() {
		if incomingOpen {
			_ = windows.CloseHandle(incomingHandle)
		}
	}()

	var rotatedHandle windows.Handle
	rotatedOpen := false
	defer func() {
		if rotatedOpen {
			_ = windows.CloseHandle(rotatedHandle)
		}
	}()

	var landingErr error
	var rollbackErr error
	ops := osRestoreFileOps
	ops.rename = func(oldPath, newPath string) error {
		renameErr := os.Rename(oldPath, newPath)
		if oldPath == incoming && newPath == controlPath {
			landingErr = renameErr
		}
		if oldPath == rotated && newPath == controlPath {
			rollbackErr = renameErr
		}
		if renameErr != nil {
			return renameErr
		}
		if oldPath == controlPath && newPath == rotated {
			rotatedHandle, renameErr = openWithoutDeleteSharing(rotated)
			if renameErr != nil {
				return fmt.Errorf("持有真实轮换副本: %w", renameErr)
			}
			rotatedOpen = true
		}
		return nil
	}

	_, err = placeRestoreCandidate(controlPath, incoming, rotated, ops)
	var continuityErr *restoreContinuityError
	if !errors.As(err, &continuityErr) {
		t.Fatalf("真实落位与回滚双失败未进入连续性 fail-closed: %v", err)
	}
	if !errors.Is(landingErr, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("候选落位未由真实 Windows sharing violation 拒绝: %v", landingErr)
	}
	if !errors.Is(rollbackErr, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("旧库回滚未由真实 Windows sharing violation 拒绝: %v", rollbackErr)
	}
	if !strings.Contains(err.Error(), "落位恢复候选") || !strings.Contains(err.Error(), "回滚当前 control.db") {
		t.Fatalf("双失败没有保留两个可诊断阶段: %v", err)
	}
	if _, statErr := os.Stat(controlPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("双失败夹具未形成 control.db 缺失边界: %v", statErr)
	}
	if content, readErr := os.ReadFile(rotated); readErr != nil || string(content) != "current-user-facts" {
		t.Fatalf("轮换副本未保留当前用户事实: content=%q err=%v", content, readErr)
	}
	if content, readErr := os.ReadFile(incoming); readErr != nil || string(content) != "restored-user-facts" {
		t.Fatalf("恢复候选未保留: content=%q err=%v", content, readErr)
	}

	dirs := appdirs.Dirs{State: filepath.Join(root, "state")}
	if err := os.MkdirAll(dirs.State, 0o700); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dirs.State, restorePendingFile)
	if err := os.WriteFile(markerPath, []byte(`{"backupId":"real-windows-double-failure"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	handleErr := handleRestoreApplyFailure(dirs, markerPath, "real-windows-double-failure", err)
	var structured *fault.Error
	if !errors.As(handleErr, &structured) || structured.Code != fault.CodeRestoreFailed {
		t.Fatalf("真实连续性双失败未映射为 RESTORE_FAILED: %v", handleErr)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("真实连续性双失败不应消费 pending: %v", statErr)
	}
	lastData, readErr := os.ReadFile(filepath.Join(dirs.State, restoreLastFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var last restoreLast
	if unmarshalErr := json.Unmarshal(lastData, &last); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if last.Applied ||
		!strings.Contains(last.Detail, "落位恢复候选") ||
		!strings.Contains(last.Detail, "回滚当前 control.db") {
		t.Fatalf("真实连续性双失败记录不完整: %+v", last)
	}

	if err := windows.CloseHandle(incomingHandle); err != nil {
		t.Fatal(err)
	}
	incomingOpen = false
	if err := windows.CloseHandle(rotatedHandle); err != nil {
		t.Fatal(err)
	}
	rotatedOpen = false
	if err := os.Rename(rotated, controlPath); err != nil {
		t.Fatalf("解除真实 OS 阻断后无法恢复旧库: %v", err)
	}
	if content, readErr := os.ReadFile(filepath.Clean(controlPath)); readErr != nil || string(content) != "current-user-facts" {
		t.Fatalf("解除阻断后当前用户事实未恢复: content=%q err=%v", content, readErr)
	}
}
