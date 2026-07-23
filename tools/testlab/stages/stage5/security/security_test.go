package security_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/auth"
	"github.com/RecRivenVI/gallery/internal/backup"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/rules"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

func TestStage5SecurityCorrectnessReport(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manual := clock.NewManual(time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC))
	manager, err := auth.NewPersonal(store.Control.SQL(), manual, identity.NewGenerator(manual), nil)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := manager.InitializeLANOwner(ctx, auth.CreateUserInput{Username: "owner", DisplayName: "Owner", Password: "owner-password-strong"})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := manager.Login(ctx, "owner", "owner-password-strong", "testlab", "synthetic-peer")
	if err != nil {
		t.Fatal(err)
	}
	expires := manual.Now().Add(time.Hour)
	token, err := manager.CreateAPIToken(ctx, session, "testlab", []string{"library.read"}, []auth.ResourceScope{{Kind: "global"}}, &expires)
	if err != nil {
		t.Fatal(err)
	}
	record := report.Report{SchemaVersion: 2, Scenario: "stage5-security-correctness", Tier: "integration", Transport: "in-process-api-services"}
	record.Add("lan-owner-initialized", owner.ID != "", "")
	record.Add("session-authenticated", session.PrincipalID == owner.ID, "")
	_, tokenErr := manager.AuthenticateAPIToken(ctx, token.Secret)
	record.Add("api-token-authenticated", tokenErr == nil, "")
	var storedHash string
	_ = store.Control.SQL().QueryRowContext(ctx, "SELECT secret_hash FROM api_tokens WHERE token_id=?", token.Token.ID).Scan(&storedHash)
	record.Add("api-token-secret-hash-only", len(storedHash) == 64 && !strings.Contains(storedHash, token.Secret), "")

	// 路径攻击必须在进入任何媒体读取前被拒绝；同时验证未触碰 Source 根外文件。
	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(sourceRoot), "outside.bin")
	outsideBody := []byte("outside-must-remain-unchanged")
	if err := os.WriteFile(outside, outsideBody, 0o600); err != nil {
		t.Fatal(err)
	}
	_, pathErr := media.PrepareSnapshot(sourceRoot, "../outside.bin", "sha256-v1", strings.Repeat("0", 64), int64(len(outsideBody)), dirs.Temp)
	var pathFault *fault.Error
	pathRejected := errors.As(pathErr, &pathFault) && pathFault.Code == fault.CodePathEscape
	afterOutside, _ := os.ReadFile(outside)
	record.Add("malicious-relative-path-rejected", pathRejected && bytes.Equal(afterOutside, outsideBody), "")

	// 媒体正文按不可信字节处理：HTML、NUL 与压缩文件 magic 不参与解释，快照只做完整
	// SHA-256/大小校验并原样复制到 AppDirs 临时区。
	mediaBody := []byte("<script>alert(1)</script>\x00PK\x03\x04\xff\xfe")
	mediaPath := filepath.Join(sourceRoot, "payload.bin")
	if err := os.WriteFile(mediaPath, mediaBody, 0o400); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(mediaBody)
	snapshot, snapshotErr := media.PrepareSnapshot(sourceRoot, "payload.bin", "sha256-v1", hex.EncodeToString(sum[:]), int64(len(mediaBody)), dirs.Temp)
	mediaExact := false
	if snapshotErr == nil {
		copied, readErr := io.ReadAll(snapshot.File)
		mediaExact = readErr == nil && bytes.Equal(copied, mediaBody)
		_ = snapshot.Close()
	}
	record.Add("malicious-media-body-treated-as-bytes", mediaExact, "")

	// metadata 中的 HTML/路径样式字符串只能作为字段值；规则 trace 不得回显原文。
	lifecycle, err := rules.NewLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	metadataPayload := `<script src=//evil.invalid/x></script>../../escape`
	dryRun, metadataErr := lifecycle.DryRun(ctx, []byte(stage5MetadataRule), []byte(`{}`), rules.DryRunInput{
		Path: "work", Metadata: map[string]any{"title": metadataPayload}, Files: []rules.DryRunFile{{Path: "payload.bin", Size: int64(len(mediaBody))}},
	})
	traceJSON, _ := json.Marshal(dryRun.Trace)
	record.Add("malicious-metadata-remains-data", metadataErr == nil && dryRun.Work.Title == metadataPayload && !bytes.Contains(traceJSON, []byte(metadataPayload)), "")

	// 启动期恢复标记属于不可信输入；路径样式 backupId 必须在路径拼接前拒绝并保持当前库。
	if err := os.WriteFile(filepath.Join(dirs.State, "restore-pending.json"),
		[]byte(`{"backupId":"../../outside","requestedBy":"attacker","requestedAt":"2026-07-23T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restoreOutcome, restoreErr := backup.ApplyPendingRestore(ctx, dirs)
	_, markerErr := os.Stat(filepath.Join(dirs.State, "restore-pending.json"))
	record.Add("malicious-restore-id-rejected", restoreErr == nil && !restoreOutcome.Applied && errors.Is(markerErr, os.ErrNotExist), "")
	if record.FailureCount != 0 {
		t.Fatalf("阶段 5 testlab findings 失败: %+v", record.Findings)
	}
	path := filepath.Join(t.TempDir(), "stage5-security.json")
	if err := record.Save(path); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token.Secret) || strings.Contains(string(encoded), dirs.Data) {
		t.Fatal("testlab 报告泄露 secret 或 AppDirs")
	}
}

const stage5MetadataRule = `{
  "rule_set_id":"rset_018f47d2-5c16-7a44-a8a0-0000000000f5","version":"1.0.0",
  "schema_version":1,"normalization_algorithm_version":"gallery-canonical-json-v1","compiler_requirement":"gallery-rule-compiler-v1","cel_profile_version":"gallery-cel-v1",
  "parameter_schema":{"type":"object","additionalProperties":false},"provider_namespaces":[],
  "primitives":[
    {"id":"work","kind":"path_match","config":{"scope":"work_directory","glob":"*","title":"directory_name","stable_key":"relative_path"}},
    {"id":"title","kind":"selector","config":{"target":"title","pointers":["/title"],"required":true}},
    {"id":"media","kind":"media_classify","config":{"glob":"*.bin","kind":"image","mime":"application/octet-stream"}}
  ],
  "cel_expressions":[],"tests":[{"id":"stage5-malicious-metadata"}],"extensions":{}
}`
