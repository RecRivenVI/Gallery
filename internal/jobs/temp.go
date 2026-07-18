package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/ports"
)

const jobTempManifestVersion = 1

type tempManifest struct {
	Version         int      `json:"version"`
	JobID           string   `json:"jobId"`
	Attempt         int      `json:"attempt"`
	TaskType        string   `json:"taskType"`
	CreatedAt       int64    `json:"createdAt"`
	ExpectedOutputs []string `json:"expectedOutputs"`
}

type TempSweepReport struct {
	TerminalRemoved int
	OrphanRemoved   int
}

// TempStore 管理 AppDirs/Temp/jobs/<job_id>/<attempt>，通用 GC 不再猜测 *.tmp 前缀。
type TempStore struct {
	db    *sql.DB
	root  string
	clock ports.Clock
}

func NewTempStore(db *sql.DB, tempRoot string, clock ports.Clock) (*TempStore, error) {
	if db == nil || strings.TrimSpace(tempRoot) == "" || clock == nil {
		return nil, fmt.Errorf("Job Temp Store 缺少依赖")
	}
	root := filepath.Join(filepath.Clean(tempRoot), "jobs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return &TempStore{db: db, root: root, clock: clock}, nil
}

func (s *TempStore) Acquire(ctx context.Context, job Job, expectedOutputs []string) (string, error) {
	if job.ID == "" || job.Attempt < 1 || job.Type == "" {
		return "", fault.New(fault.CodeValidation, false, nil)
	}
	for _, output := range expectedOutputs {
		clean := filepath.Clean(filepath.FromSlash(output))
		if output == "" || clean == "." || clean == ".." || filepath.IsAbs(clean) ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fault.New(fault.CodePathEscape, false, nil)
		}
	}
	directory := filepath.Join(s.root, job.ID, strconv.Itoa(job.Attempt))
	if !s.withinRoot(directory) {
		return "", fault.New(fault.CodePathEscape, false, nil)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	manifest := tempManifest{Version: jobTempManifestVersion, JobID: job.ID, Attempt: job.Attempt,
		TaskType: job.Type, CreatedAt: now.Unix(), ExpectedOutputs: append([]string(nil), expectedOutputs...)}
	if err := writeTempManifest(directory, manifest); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	outputs, _ := json.Marshal(manifest.ExpectedOutputs)
	relative, _ := filepath.Rel(s.root, directory)
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_temp_directories
(job_id, attempt, task_type, relative_path, manifest_version, expected_outputs_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(job_id, attempt) DO UPDATE SET task_type=excluded.task_type,
relative_path=excluded.relative_path, manifest_version=excluded.manifest_version,
expected_outputs_json=excluded.expected_outputs_json, updated_at=excluded.updated_at`,
		job.ID, job.Attempt, job.Type, filepath.ToSlash(relative), jobTempManifestVersion,
		string(outputs), now.Unix(), now.Unix())
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return directory, nil
}

func (s *TempStore) Sweep(ctx context.Context, terminalGrace, orphanGrace time.Duration) (TempSweepReport, error) {
	if terminalGrace < 0 || orphanGrace < terminalGrace {
		return TempSweepReport{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT d.job_id, d.attempt, d.relative_path, d.updated_at, a.status
FROM job_temp_directories d
JOIN job_attempts a ON a.job_id=d.job_id AND a.attempt=d.attempt
ORDER BY d.job_id, d.attempt`)
	if err != nil {
		return TempSweepReport{}, fault.New(fault.CodeInternal, true, err)
	}
	type record struct {
		jobID, relative, status string
		attempt                 int
		updated                 int64
	}
	var records []record
	registered := make(map[string]struct{})
	for rows.Next() {
		var item record
		if err := rows.Scan(&item.jobID, &item.attempt, &item.relative, &item.updated, &item.status); err != nil {
			rows.Close()
			return TempSweepReport{}, fault.New(fault.CodeInternal, true, err)
		}
		records = append(records, item)
		registered[filepath.Clean(filepath.Join(s.root, filepath.FromSlash(item.relative)))] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return TempSweepReport{}, fault.New(fault.CodeInternal, true, err)
	}
	report := TempSweepReport{}
	for _, item := range records {
		if item.status == "queued" || item.status == "running" || item.updated > now.Add(-terminalGrace).Unix() {
			continue
		}
		directory := filepath.Clean(filepath.Join(s.root, filepath.FromSlash(item.relative)))
		manifest, err := readTempManifest(filepath.Join(directory, "manifest.json"))
		if err != nil || manifest.JobID != item.jobID || manifest.Attempt != item.attempt ||
			manifest.Version != jobTempManifestVersion {
			if item.updated > now.Add(-orphanGrace).Unix() {
				continue
			}
			if err := s.removeOwned(directory); err != nil {
				return report, err
			}
			if _, err := s.db.ExecContext(ctx, "DELETE FROM job_temp_directories WHERE job_id=? AND attempt=?",
				item.jobID, item.attempt); err != nil {
				return report, fault.New(fault.CodeInternal, true, err)
			}
			report.OrphanRemoved++
			continue
		}
		if err := s.removeOwned(directory); err != nil {
			return report, err
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM job_temp_directories WHERE job_id=? AND attempt=?",
			item.jobID, item.attempt); err != nil {
			return report, fault.New(fault.CodeInternal, true, err)
		}
		report.TerminalRemoved++
	}
	jobDirs, _ := os.ReadDir(s.root)
	for _, jobDir := range jobDirs {
		if !jobDir.IsDir() || jobDir.Type()&os.ModeSymlink != 0 {
			continue
		}
		attemptDirs, _ := os.ReadDir(filepath.Join(s.root, jobDir.Name()))
		for _, attemptDir := range attemptDirs {
			if !attemptDir.IsDir() || attemptDir.Type()&os.ModeSymlink != 0 {
				continue
			}
			directory := filepath.Join(s.root, jobDir.Name(), attemptDir.Name())
			if _, exists := registered[filepath.Clean(directory)]; exists {
				continue
			}
			info, err := attemptDir.Info()
			if err != nil || info.ModTime().After(now.Add(-orphanGrace)) {
				continue
			}
			if err := s.removeOwned(directory); err != nil {
				return report, err
			}
			report.OrphanRemoved++
		}
	}
	return report, nil
}

func (s *TempStore) removeOwned(target string) error {
	if !s.withinRoot(target) {
		return fault.New(fault.CodePathEscape, false, nil)
	}
	relative, _ := filepath.Rel(s.root, target)
	current := s.root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fault.New(fault.CodePathEscape, false, nil)
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *TempStore) withinRoot(target string) bool {
	relative, err := filepath.Rel(s.root, filepath.Clean(target))
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func writeTempManifest(directory string, value tempManifest) error {
	temporary, err := os.CreateTemp(directory, "manifest-*.tmp")
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	target := filepath.Join(directory, "manifest.json")
	_ = os.Remove(target)
	return os.Rename(temporary.Name(), target)
}

func readTempManifest(path string) (tempManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return tempManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var value tempManifest
	if err := decoder.Decode(&value); err != nil {
		return tempManifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return tempManifest{}, fmt.Errorf("manifest 包含额外 JSON")
	}
	return value, nil
}
