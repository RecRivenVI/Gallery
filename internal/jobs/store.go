package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusPublishing  Status = "publishing"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusNeedsRepair Status = "needs_repair"
)

type Job struct {
	ID               string
	Type             string
	SourceID         string
	CreatedBy        string
	Status           Status
	Stage            string
	ProgressCurrent  int64
	ProgressTotal    int64
	ProgressSequence uint64
	IssueCode        string
	PublicationID    string
	RetryOf          string
	Attempt          int
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	UpdatedAt        time.Time
}

type Store struct {
	db    *sql.DB
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewStore(db *sql.DB, clock ports.Clock, ids ports.IDGenerator) (*Store, error) {
	if db == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("Job Store 缺少依赖")
	}
	return &Store{db: db, clock: clock, ids: ids}, nil
}

func (s *Store) CreateScan(ctx context.Context, sourceID, createdBy, retryOf string) (Job, error) {
	if _, err := domain.ParseID(domain.IDSource, sourceID); err != nil || strings.TrimSpace(createdBy) == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	attempt := 1
	if retryOf != "" {
		previous, err := s.Get(ctx, retryOf)
		if err != nil {
			return Job{}, err
		}
		if previous.SourceID != sourceID || (previous.Status != StatusFailed && previous.Status != StatusCancelled && previous.Status != StatusNeedsRepair) {
			return Job{}, fault.New(fault.CodeJobStateConflict, false, nil)
		}
		attempt = previous.Attempt + 1
	}
	id, err := s.ids.New(domain.IDJob)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	job := Job{
		ID: id.String(), Type: "scan", SourceID: sourceID, CreatedBy: createdBy,
		Status: StatusQueued, Stage: "queued", ProgressSequence: 1, RetryOf: retryOf,
		Attempt: attempt, CreatedAt: now, UpdatedAt: now,
	}
	var retry any
	if retryOf != "" {
		retry = retryOf
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO jobs
(job_id, job_type, source_id, created_by, status, stage, progress_sequence, retry_of, attempt, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Type, job.SourceID, job.CreatedBy,
		job.Status, job.Stage, job.ProgressSequence, retry, job.Attempt, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Job{}, fault.New(fault.CodeScanAlreadyRunning, true, nil)
		}
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	return job, nil
}

func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	if _, err := domain.ParseID(domain.IDJob, id); err != nil {
		return Job{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var job Job
	var sourceID, issueCode, publicationID, retryOf sql.NullString
	var startedAt, finishedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT job_id, job_type, source_id, created_by, status, stage,
       progress_current, progress_total, progress_sequence, issue_code, publication_id,
       retry_of, attempt, created_at, started_at, finished_at, updated_at
FROM jobs WHERE job_id = ?`, id).Scan(
		&job.ID, &job.Type, &sourceID, &job.CreatedBy, &job.Status, &job.Stage,
		&job.ProgressCurrent, &job.ProgressTotal, &job.ProgressSequence, &issueCode, &publicationID,
		&retryOf, &job.Attempt, &createdAt, &startedAt, &finishedAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	job.SourceID, job.IssueCode, job.PublicationID, job.RetryOf = sourceID.String, issueCode.String, publicationID.String, retryOf.String
	job.CreatedAt, job.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	job.StartedAt = nullableTime(startedAt)
	job.FinishedAt = nullableTime(finishedAt)
	return job, nil
}

func (s *Store) Start(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusQueued, StatusRunning, "discovering", `started_at = ?,`, []any{now.Unix()}, now)
}

func (s *Store) Progress(ctx context.Context, id, stage string, current, total int64) (Job, error) {
	if stage == "" || current < 0 || total < 0 || (total > 0 && current > total) {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET stage = ?, progress_current = ?, progress_total = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status = ?`, stage, current, total, now.Unix(), id, StatusRunning)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) BeginPublishing(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusRunning, StatusPublishing, "publishing", "", nil, now)
}

func (s *Store) Complete(ctx context.Context, id, publicationID string) (Job, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, publicationID); err != nil {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	return s.transition(ctx, id, StatusPublishing, StatusCompleted, "completed", `publication_id = ?, finished_at = ?,`, []any{publicationID, now.Unix()}, now)
}

func (s *Store) RecoverCompleted(ctx context.Context, id, publicationID string) (Job, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, publicationID); err != nil {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'completed', publication_id = ?, finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?)`, StatusCompleted, publicationID, now.Unix(), now.Unix(), id, StatusRunning, StatusPublishing)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Fail(ctx context.Context, id, issueCode string) (Job, error) {
	if issueCode == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'failed', issue_code = ?, finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?, ?)`, StatusFailed, issueCode, now.Unix(), now.Unix(), id, StatusQueued, StatusRunning, StatusPublishing)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) MarkNeedsRepair(ctx context.Context, id, issueCode string) (Job, error) {
	if issueCode == "" {
		return Job{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'needs_repair', issue_code = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status = ?`, StatusNeedsRepair, issueCode, now.Unix(), id, StatusCompleted)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Cancel(ctx context.Context, id string) (Job, error) {
	now := s.clock.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE jobs SET status = ?, stage = 'cancelled', finished_at = ?,
                progress_sequence = progress_sequence + 1, updated_at = ?
WHERE job_id = ? AND status IN (?, ?)`, StatusCancelled, now.Unix(), now.Unix(), id, StatusQueued, StatusRunning)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) ListByStatuses(ctx context.Context, statuses ...Status) ([]Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for index, status := range statuses {
		placeholders[index], args[index] = "?", status
	}
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM jobs WHERE status IN ("+strings.Join(placeholders, ",")+") ORDER BY created_at, job_id", args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	result := make([]Job, 0, len(ids))
	for _, id := range ids {
		job, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, nil
}

func (s *Store) transition(ctx context.Context, id string, from, to Status, stage, assignments string, values []any, now time.Time) (Job, error) {
	query := "UPDATE jobs SET status = ?, stage = ?, " + assignments + " progress_sequence = progress_sequence + 1, updated_at = ? WHERE job_id = ? AND status = ?"
	args := []any{to, stage}
	args = append(args, values...)
	args = append(args, now.Unix(), id, from)
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return Job{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := requireOne(result); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, id)
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if count != 1 {
		return fault.New(fault.CodeJobStateConflict, false, nil)
	}
	return nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}
