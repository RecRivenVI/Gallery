package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	api "github.com/RecRivenVI/gallery/api"
)

type fakeResponse struct {
	code int
}

func (response *fakeResponse) StatusCode() int {
	return response.code
}

func TestStatusCodeHandlesTypedNil(t *testing.T) {
	var response *fakeResponse
	if got := statusCode(response); got != 0 {
		t.Fatalf("typed nil status=%d", got)
	}
	if got := statusCode(&fakeResponse{code: 202}); got != 202 {
		t.Fatalf("status=%d", got)
	}
}

func TestValidateLibraryPresence(t *testing.T) {
	libraries := []api.Library{{Name: beforeBackupLibrary}, {Name: afterBackupLibrary}}
	if err := validateLibraryPresence(libraries, map[string]bool{
		beforeBackupLibrary: true,
		afterBackupLibrary:  true,
		"missing":           false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateLibraryPresence(libraries, map[string]bool{"missing": true}); err == nil {
		t.Fatal("缺失 Library 未被拒绝")
	}
}

func TestCorruptBackupStaysInsideAppRoot(t *testing.T) {
	root := t.TempDir()
	if err := corruptBackup(root, "../../outside"); err == nil {
		t.Fatal("路径样式 backup ID 未被拒绝")
	}
	backupID := "bkp_00000000-0000-7000-8000-000000000001"
	directory := filepath.Join(root, "state", "backups", backupID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "control.db")
	if err := os.WriteFile(path, []byte("valid fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := corruptBackup(root, backupID); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "intentional corrupt backup fixture" {
		t.Fatalf("备份未被精确替换: %q", content)
	}
}

func TestAssertFailedRestoreRecorded(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	backupID := "bkp_00000000-0000-7000-8000-000000000001"
	content := []byte(`{"backupId":"` + backupID + `","applied":false,"detail":"checksum mismatch"}`)
	if err := os.WriteFile(filepath.Join(state, "restore-last.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := assertFailedRestoreRecorded(root, backupID, "checksum"); err != nil {
		t.Fatal(err)
	}
	if err := assertFailedRestoreRecorded(root, backupID, "轮换当前 control.db"); err == nil {
		t.Fatal("不匹配的失败阶段未被拒绝")
	}
	if err := os.WriteFile(filepath.Join(state, "restore-pending.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := assertFailedRestoreRecorded(root, backupID, ""); err == nil {
		t.Fatal("未消费的恢复标记未被拒绝")
	}
}

func TestAssertContinuityRestoreRecorded(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	backupID := "bkp_00000000-0000-7000-8000-000000000001"
	if err := os.WriteFile(
		filepath.Join(state, "restore-pending.json"),
		[]byte(`{"backupId":"`+backupID+`"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(state, "restore-last.json"),
		[]byte(`{"backupId":"`+backupID+`","applied":false,"detail":"落位恢复候选 failed"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := assertContinuityRestoreRecorded(root, backupID, "落位恢复候选"); err != nil {
		t.Fatal(err)
	}
	if err := assertContinuityRestoreRecorded(root, backupID, "回滚当前 control.db"); err == nil {
		t.Fatal("不匹配的连续性失败阶段未被拒绝")
	}
}

func TestAssertSuccessfulRestoreRecorded(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	backupID := "bkp_00000000-0000-7000-8000-000000000001"
	if err := os.WriteFile(
		filepath.Join(state, "restore-last.json"),
		[]byte(`{"backupId":"`+backupID+`","applied":true,"detail":"已原子替换 control.db"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := assertSuccessfulRestoreRecorded(root, backupID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "restore-pending.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := assertSuccessfulRestoreRecorded(root, backupID); err == nil {
		t.Fatal("成功恢复后未消费的 pending 未被拒绝")
	}
}

func TestWriteFinalizeResumeMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	backupID := "bkp_00000000-0000-7000-8000-000000000001"
	if err := writeFinalizeResumeMarker(root, backupID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "state", "restore-pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		BackupID string `json:"backupId"`
		Phase    string `json:"phase"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.BackupID != backupID || marker.Phase != "placed_pending_finalize" {
		t.Fatalf("待 finalize marker 不精确: %+v", marker)
	}
	if err := assertPendingFinalizeMarker(root, backupID); err != nil {
		t.Fatal(err)
	}
}

func TestBlockRestoreOutcomePath(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	lastPath := filepath.Join(state, "restore-last.json")
	if err := os.WriteFile(lastPath, []byte(`{"applied":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := blockRestoreOutcomePath(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(lastPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("恢复结果路径未替换为目录阻断: info=%v err=%v", info, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lastPath); !os.IsNotExist(err) {
		t.Fatalf("恢复结果目录阻断未解除: %v", err)
	}
}
