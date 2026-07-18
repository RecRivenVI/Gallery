package watcher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type ScanScheduler interface {
	CreateScan(ctx context.Context, sourceID, createdBy string) (jobs.Job, error)
	Start(jobID string)
}

type State struct {
	SourceID             string
	Status               string
	Dirty                bool
	WatcherAvailable     bool
	WatcherOverflow      bool
	LastEventAt          *time.Time
	LastCheckedAt        *time.Time
	CurrentJobID         string
	PendingHashCount     int64
	BlockingIssueCode    string
	CurrentPublicationID string
	UpdatedAt            time.Time
}

type Service struct {
	context   context.Context
	control   *sql.DB
	resources *application.Resources
	jobs      *jobs.Store
	scanner   ScanScheduler
	watcher   ports.FileWatcher
	clock     ports.Clock
	interval  time.Duration

	mu       sync.Mutex
	started  bool
	managed  map[string]*managedWatcher
	retryMin time.Duration
	retryMax time.Duration
	wait     sync.WaitGroup
}

type managedWatcher struct {
	root   string
	cancel context.CancelFunc
}

type sourceDescriptor struct {
	id   string
	root string
}

func New(ctx context.Context, control *sql.DB, resources *application.Resources, jobStore *jobs.Store, scannerService ScanScheduler, fileWatcher ports.FileWatcher, clock ports.Clock, interval time.Duration) (*Service, error) {
	if ctx == nil || control == nil || resources == nil || jobStore == nil || scannerService == nil || clock == nil {
		return nil, fmt.Errorf("Watcher Service 缺少依赖")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Service{context: ctx, control: control, resources: resources, jobs: jobStore, scanner: scannerService,
		watcher: fileWatcher, clock: clock, interval: interval, managed: make(map[string]*managedWatcher),
		retryMin: time.Second, retryMax: time.Minute}, nil
}

// SetRetryPolicy 配置 Watcher 失败重启退避；属于 pre-freeze 运行参数。
func (s *Service) SetRetryPolicy(minimum, maximum time.Duration) {
	if minimum <= 0 || maximum < minimum {
		return
	}
	s.mu.Lock()
	s.retryMin, s.retryMax = minimum, maximum
	s.mu.Unlock()
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	if ctx == nil {
		ctx = s.context
	}
	s.wait.Add(1)
	go func() {
		defer s.wait.Done()
		s.run(ctx)
	}()
}

func (s *Service) Wait() { s.wait.Wait() }

func (s *Service) run(ctx context.Context) {
	defer s.stopManaged()
	_ = s.syncManaged(ctx)
	_ = s.ReconcileAll(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.syncManaged(ctx)
			ids, err := s.sourceIDs(ctx)
			if err != nil {
				continue
			}
			for _, sourceID := range ids {
				_ = s.ReconcileSource(ctx, sourceID)
			}
		}
	}
}

func (s *Service) watchSource(ctx context.Context, sourceID, root string) {
	backoff := s.retryMin
	for {
		if ctx.Err() != nil {
			return
		}
		events, err := s.watcher.Watch(ctx, root)
		if err == nil {
			_ = s.updateState(ctx, sourceID, func(state *State) {
				state.Status = "online"
				state.WatcherAvailable = true
				state.BlockingIssueCode = ""
			})
			backoff = s.retryMin
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-events:
					if !ok {
						err = errors.New("Watcher event channel 已关闭")
						goto restart
					}
					_ = s.HandleEvent(ctx, sourceID, event)
				}
			}
		}
	restart:
		_ = s.updateState(ctx, sourceID, func(state *State) {
			state.WatcherAvailable = false
			state.Dirty = true
			state.BlockingIssueCode = string(fault.CodeSourceUnavailable)
		})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < s.retryMax {
			backoff *= 2
			if backoff > s.retryMax {
				backoff = s.retryMax
			}
		}
	}
}

func (s *Service) syncManaged(ctx context.Context) error {
	if s.watcher == nil {
		return nil
	}
	descriptors, err := s.sourceDescriptors(ctx)
	if err != nil {
		return err
	}
	wanted := make(map[string]string, len(descriptors))
	for _, item := range descriptors {
		wanted[item.id] = item.root
	}
	s.mu.Lock()
	for id, current := range s.managed {
		root, exists := wanted[id]
		if !exists || root != current.root {
			current.cancel()
			delete(s.managed, id)
		}
	}
	for _, item := range descriptors {
		if _, exists := s.managed[item.id]; exists {
			continue
		}
		watchContext, cancel := context.WithCancel(ctx)
		s.managed[item.id] = &managedWatcher{root: item.root, cancel: cancel}
		s.wait.Add(1)
		go func(sourceID, root string) {
			defer s.wait.Done()
			s.watchSource(watchContext, sourceID, root)
		}(item.id, item.root)
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) stopManaged() {
	s.mu.Lock()
	for id, current := range s.managed {
		current.cancel()
		delete(s.managed, id)
	}
	s.mu.Unlock()
}

func (s *Service) HandleEvent(ctx context.Context, sourceID string, event ports.WatchEvent) error {
	if strings.TrimSpace(sourceID) == "" {
		return fault.New(fault.CodeValidation, false, nil)
	}
	return s.updateState(ctx, sourceID, func(state *State) {
		state.Dirty = true
		state.Status = "online"
		state.LastEventAt = timePtr(event.At.UTC())
		if event.Overflow || event.Kind == ports.WatchOverflow {
			state.WatcherOverflow = true
			state.BlockingIssueCode = string(fault.CodeWatcherOverflow)
		}
	})
}

func (s *Service) ReconcileAll(ctx context.Context) error {
	ids, err := s.sourceIDs(ctx)
	if err != nil {
		return err
	}
	for _, sourceID := range ids {
		if err := s.ReconcileSource(ctx, sourceID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ReconcileSource(ctx context.Context, sourceID string) error {
	source, err := s.resources.GetSource(ctx, sourceID)
	if err != nil {
		return err
	}
	state, err := s.GetState(ctx, sourceID)
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	state.LastCheckedAt = &now
	available := s.resources.SourceAvailable(source)
	if !available {
		state.Status = "offline"
		state.Dirty = true
		state.BlockingIssueCode = string(fault.CodeSourceUnavailable)
		return s.saveState(ctx, state)
	}
	if state.Status != "online" {
		state.Dirty = true
	}
	state.Status = "online"
	active, err := s.activeScan(ctx, sourceID)
	if err != nil {
		return err
	}
	if state.CurrentJobID != "" && !active {
		job, jobErr := s.jobs.Get(ctx, state.CurrentJobID)
		if jobErr == nil {
			switch job.Status {
			case jobs.StatusCompleted:
				state.Dirty = false
				state.BlockingIssueCode = ""
			case jobs.StatusFailed, jobs.StatusCancelled, jobs.StatusNeedsRepair:
				state.Dirty = true
				state.BlockingIssueCode = job.IssueCode
			}
		}
		state.CurrentJobID = ""
	}
	if state.Dirty && !active {
		job, createErr := s.scanner.CreateScan(ctx, sourceID, "system-watcher")
		if createErr != nil {
			state.BlockingIssueCode = faultCode(createErr)
			return s.saveState(ctx, state)
		}
		state.CurrentJobID = job.ID
		state.Dirty = false
		state.BlockingIssueCode = ""
		if err := s.saveState(ctx, state); err != nil {
			return err
		}
		s.scanner.Start(job.ID)
		return nil
	}
	return s.saveState(ctx, state)
}

func (s *Service) GetState(ctx context.Context, sourceID string) (State, error) {
	var state State
	var currentJobID, issue, publication sql.NullString
	var lastEvent, lastChecked sql.NullInt64
	var updated int64
	var dirty, watcherAvailable, overflow int
	err := s.control.QueryRowContext(ctx, `SELECT source_id, status, dirty, watcher_available, watcher_overflow,
last_event_at, last_checked_at, current_job_id, pending_hash_count, blocking_issue_code, current_publication_id, updated_at
FROM source_scan_states WHERE source_id=?`, sourceID).Scan(&state.SourceID, &state.Status, &dirty, &watcherAvailable, &overflow,
		&lastEvent, &lastChecked, &currentJobID, &state.PendingHashCount, &issue, &publication, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		now := s.clock.Now().UTC()
		return State{SourceID: sourceID, Status: "unknown", Dirty: true, LastCheckedAt: &now, UpdatedAt: now}, nil
	}
	if err != nil {
		return State{}, fault.New(fault.CodeInternal, true, err)
	}
	state.Dirty, state.WatcherAvailable, state.WatcherOverflow = dirty != 0, watcherAvailable != 0, overflow != 0
	state.CurrentJobID, state.BlockingIssueCode, state.CurrentPublicationID = currentJobID.String, issue.String, publication.String
	state.LastEventAt, state.LastCheckedAt, state.UpdatedAt = nullableTime(lastEvent), nullableTime(lastChecked), time.Unix(updated, 0).UTC()
	return state, nil
}

func (s *Service) updateState(ctx context.Context, sourceID string, mutate func(*State)) error {
	state, err := s.GetState(ctx, sourceID)
	if err != nil {
		return err
	}
	mutate(&state)
	return s.saveState(ctx, state)
}

func (s *Service) saveState(ctx context.Context, state State) error {
	now := s.clock.Now().UTC()
	state.UpdatedAt = now
	_, err := s.control.ExecContext(ctx, `INSERT INTO source_scan_states
(source_id, status, dirty, watcher_available, watcher_overflow, last_event_at, last_checked_at, current_job_id,
pending_hash_count, blocking_issue_code, current_publication_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id) DO UPDATE SET status=excluded.status, dirty=excluded.dirty,
watcher_available=excluded.watcher_available, watcher_overflow=excluded.watcher_overflow,
last_event_at=excluded.last_event_at, last_checked_at=excluded.last_checked_at, current_job_id=excluded.current_job_id,
pending_hash_count=excluded.pending_hash_count, blocking_issue_code=excluded.blocking_issue_code,
current_publication_id=excluded.current_publication_id, updated_at=excluded.updated_at`, state.SourceID, state.Status,
		boolInt(state.Dirty), boolInt(state.WatcherAvailable), boolInt(state.WatcherOverflow), nullableTimeValue(state.LastEventAt),
		nullableTimeValue(state.LastCheckedAt), nullableStringValue(state.CurrentJobID), state.PendingHashCount,
		nullableStringValue(state.BlockingIssueCode), nullableStringValue(state.CurrentPublicationID), now.Unix())
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Service) activeScan(ctx context.Context, sourceID string) (bool, error) {
	items, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing, jobs.StatusCancelling)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.Type == "scan" && item.SourceID == sourceID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) sourceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.control.QueryContext(ctx, "SELECT source_id FROM sources ORDER BY source_id")
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s *Service) sourceDescriptors(ctx context.Context) ([]sourceDescriptor, error) {
	rows, err := s.control.QueryContext(ctx, "SELECT source_id, root_path FROM sources ORDER BY source_id")
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []sourceDescriptor
	for rows.Next() {
		var item sourceDescriptor
		if err := rows.Scan(&item.id, &item.root); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func faultCode(err error) string {
	var structured *fault.Error
	if errors.As(err, &structured) {
		return string(structured.Code)
	}
	return string(fault.CodeInternal)
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}

func timePtr(value time.Time) *time.Time { return &value }

func nullableTimeValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func nullableStringValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
