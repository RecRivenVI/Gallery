// Package backup 实现 control.db 的产品级一致性备份与（见 restore.go）安全恢复。
//
// control.db 是所有不可重建产品事实的权威库，是最高优先级备份对象。备份使用 SQLite 一致性
// 机制（VACUUM INTO），先写受控临时位置、校验完整性与 role、计算 checksum 与 manifest，再原子
// 发布到 AppDirs 内的受控备份目录。备份失败绝不触碰当前 control.db，也绝不纳入 catalog.db、
// 媒体、缓存或日志。Source 永远零写入。
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
)

const (
	// ManifestVersion 是备份 manifest 的结构版本；恢复端据此判断能否解释。
	ManifestVersion = 1
	// JobTypeBackup 是维护类备份 Job 的类型标识。
	JobTypeBackup = "control_backup"

	manifestFileName = "manifest.json"
	databaseFileName = "control.db"
	stagingPrefix    = ".staging-"
	checksumAlgo     = "sha256"
)

// Notifier 接收 Job 状态变化，用于实时事件；缺省不做任何事。
type Notifier interface {
	JobChanged(jobs.Job)
}

type nopNotifier struct{}

func (nopNotifier) JobChanged(jobs.Job) {}

// Dispatcher 把 Job 交给中央有界调度器执行。未注入时回退到自管理 goroutine（供单元测试使用）。
type Dispatcher interface {
	Submit(jobID string)
}

// FileEntry 描述备份内某个物理文件的身份。
type FileEntry struct {
	FileName          string `json:"fileName"`
	SizeBytes         int64  `json:"sizeBytes"`
	Checksum          string `json:"checksum"`
	ChecksumAlgorithm string `json:"checksumAlgorithm"`
}

// SecurityScope 显式声明安全敏感状态是否进入备份，避免为方便而未经说明地全部复制。
type SecurityScope struct {
	Sessions            string `json:"sessions"`
	PairingCredentials  string `json:"pairingCredentials"`
	APITokens           string `json:"apiTokens"`
	CredentialStoreRefs string `json:"credentialStoreRefs"`
	Note                string `json:"note"`
}

// Manifest 是备份自描述文件，覆盖身份、版本、大小、checksum 与安全范围。
type Manifest struct {
	BackupID          string        `json:"backupId"`
	ManifestVersion   int           `json:"manifestVersion"`
	Role              string        `json:"role"`
	AppVersion        string        `json:"appVersion"`
	SchemaVersion     int64         `json:"schemaVersion"`
	MigrationChecksum string        `json:"migrationChecksum"`
	CreatedAt         time.Time     `json:"createdAt"`
	Database          FileEntry     `json:"database"`
	Security          SecurityScope `json:"security"`
	Notes             string        `json:"notes,omitempty"`
}

// DefaultSecurityScope 是当前阶段 control.db 备份的安全范围声明。备份是 control.db 的完整逻辑
// 副本：session 与 pairing 仅含 SHA-256 摘要，恢复时统一作废；阶段 1 尚无 API Token 与
// CredentialStore 引用。
func DefaultSecurityScope() SecurityScope {
	return SecurityScope{
		Sessions:            "included-hashed",
		PairingCredentials:  "included-hashed",
		APITokens:           "not-present",
		CredentialStoreRefs: "not-present",
		Note:                "control.db 完整逻辑副本；session/pairing 仅含 SHA-256 摘要并在恢复时作废；阶段 1 无 API Token 与 CredentialStore 引用",
	}
}

type Service struct {
	ctx        context.Context
	control    *storage.Database
	jobs       *jobs.Store
	dirs       appdirs.Dirs
	clock      ports.Clock
	ids        ports.IDGenerator
	appVersion string
	notifier   Notifier
	dispatcher Dispatcher
	wait       sync.WaitGroup
}

func New(ctx context.Context, control *storage.Database, jobStore *jobs.Store, dirs appdirs.Dirs, clock ports.Clock, ids ports.IDGenerator, appVersion string, notifier Notifier) (*Service, error) {
	if ctx == nil || control == nil || jobStore == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("backup Service 缺少依赖")
	}
	if control.Role() != storage.RoleControl {
		return nil, fmt.Errorf("backup Service 需要 control 数据库")
	}
	if notifier == nil {
		notifier = nopNotifier{}
	}
	return &Service{ctx: ctx, control: control, jobs: jobStore, dirs: dirs, clock: clock, ids: ids, appVersion: appVersion, notifier: notifier}, nil
}

// SetDispatcher 注入中央调度器；注入后 Start 通过调度器领取执行并接受其 context 取消。
func (s *Service) SetDispatcher(d Dispatcher) { s.dispatcher = d }

func (s *Service) backupRoot() string { return filepath.Join(s.dirs.State, "backups") }

// CreateBackup 入库一个备份 Job。若已有活跃备份 Job，返回 JOB_STATE_CONFLICT。
func (s *Service) CreateBackup(ctx context.Context, createdBy string) (jobs.Job, error) {
	job, err := s.jobs.CreateMaintenance(ctx, JobTypeBackup, createdBy)
	if err == nil {
		s.notifier.JobChanged(job)
	}
	return job, err
}

func (s *Service) Start(jobID string) {
	if s.dispatcher != nil {
		s.dispatcher.Submit(jobID)
		return
	}
	s.wait.Add(1)
	go func() { defer s.wait.Done(); _ = s.Execute(s.ctx, jobID) }()
}

func (s *Service) Wait() { s.wait.Wait() }

// Execute 是备份 Job 的 Runner：一致性副本写入受控临时目录，校验并计算 manifest 后原子发布。
// 任何失败都清理临时目录并把 Job 标记为 failed，绝不影响当前 control.db。
func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "preparing")
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)

	backupID, err := s.ids.New(domain.IDControlBackup)
	if err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeInternal, true, err))
	}
	root := s.backupRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}
	staging := filepath.Join(root, stagingPrefix+backupID.String())
	final := filepath.Join(root, backupID.String())
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}
	cleanup := func() { _ = os.RemoveAll(staging) }

	if job, err = s.jobs.Progress(ctx, jobID, "copying", 0, 3); err != nil {
		cleanup()
		return err
	}
	s.notifier.JobChanged(job)

	dbDest := filepath.Join(staging, databaseFileName)
	if err := s.control.Backup(ctx, dbDest); err != nil {
		cleanup()
		return s.fail(ctx, jobID, err)
	}
	if ctx.Err() != nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeProcessInterrupted, true, ctx.Err()))
	}

	if job, err = s.jobs.Progress(ctx, jobID, "verifying", 1, 3); err != nil {
		cleanup()
		return err
	}
	s.notifier.JobChanged(job)

	size, checksum, err := fileChecksum(dbDest)
	if err != nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}
	schema, err := storage.ReadSchemaState(ctx, s.control.SQL())
	if err != nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}
	manifest := Manifest{
		BackupID: backupID.String(), ManifestVersion: ManifestVersion, Role: string(storage.RoleControl),
		AppVersion: s.appVersion, SchemaVersion: schema.Version, MigrationChecksum: schema.Checksum,
		CreatedAt: s.clock.Now().UTC(),
		Database:  FileEntry{FileName: databaseFileName, SizeBytes: size, Checksum: checksum, ChecksumAlgorithm: checksumAlgo},
		Security:  DefaultSecurityScope(),
	}
	if err := writeManifest(filepath.Join(staging, manifestFileName), manifest); err != nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}

	if job, err = s.jobs.Progress(ctx, jobID, "publishing", 2, 3); err != nil {
		cleanup()
		return err
	}
	s.notifier.JobChanged(job)

	if _, statErr := os.Stat(final); statErr == nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, fmt.Errorf("备份目标已存在")))
	}
	if err := os.Rename(staging, final); err != nil {
		cleanup()
		return s.fail(ctx, jobID, fault.New(fault.CodeBackupFailed, false, err))
	}

	job, err = s.jobs.CompleteMaintenance(ctx, jobID)
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
}

// List 读取全部已发布备份的 manifest，按创建时间倒序返回。未发布任何备份时返回空列表。
func (s *Service) List(ctx context.Context) ([]Manifest, error) {
	entries, err := os.ReadDir(s.backupRoot())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Manifest{}, nil
		}
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	result := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, err := readManifest(filepath.Join(s.backupRoot(), entry.Name(), manifestFileName))
		if err != nil {
			continue // 损坏或半成品目录不进入列表，但不影响其余备份。
		}
		result = append(result, manifest)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

// Get 读取单个备份的 manifest。不存在时返回 BACKUP_NOT_FOUND，损坏时返回 BACKUP_CORRUPT。
func (s *Service) Get(ctx context.Context, backupID string) (Manifest, error) {
	if _, err := domain.ParseID(domain.IDControlBackup, backupID); err != nil {
		return Manifest{}, fault.New(fault.CodeBackupNotFound, false, nil)
	}
	path := filepath.Join(s.backupRoot(), backupID, manifestFileName)
	manifest, err := readManifest(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, fault.New(fault.CodeBackupNotFound, false, nil)
		}
		return Manifest{}, fault.New(fault.CodeBackupCorrupt, false, err)
	}
	return manifest, nil
}

// Reconcile 在启动时收敛遗留备份 Job 并清理半成品临时目录。中断的备份因原子发布不会污染
// 已发布集合；未完成的 queued Job 重新入队，running/publishing Job 标记为中断。
func (s *Service) Reconcile(ctx context.Context) error {
	s.gcStaging()
	nonterminal, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing)
	if err != nil {
		return err
	}
	for _, job := range nonterminal {
		if job.Type != JobTypeBackup {
			continue
		}
		if job.Status == jobs.StatusQueued {
			s.Start(job.ID)
			continue
		}
		failed, failErr := s.jobs.Fail(ctx, job.ID, string(fault.CodeProcessInterrupted))
		if failErr != nil {
			return failErr
		}
		s.notifier.JobChanged(failed)
	}
	return nil
}

func (s *Service) gcStaging() {
	entries, err := os.ReadDir(s.backupRoot())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), stagingPrefix) {
			_ = os.RemoveAll(filepath.Join(s.backupRoot(), entry.Name()))
		}
	}
}

func (s *Service) fail(ctx context.Context, jobID string, cause error) error {
	code := fault.CodeInternal
	var structured *fault.Error
	if errors.As(cause, &structured) {
		code = structured.Code
	}
	if failed, err := s.jobs.Fail(ctx, jobID, string(code)); err == nil {
		s.notifier.JobChanged(failed)
	}
	return cause
}

func fileChecksum(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.ManifestVersion == 0 || manifest.BackupID == "" || manifest.Role == "" {
		return Manifest{}, fmt.Errorf("manifest 字段缺失")
	}
	return manifest, nil
}
