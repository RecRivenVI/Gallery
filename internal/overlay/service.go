package overlay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/ports"
	"golang.org/x/text/unicode/norm"
)

const (
	IssueProjectionFailed = "OVERLAY_PROJECTION_FAILED"
	IssueSuperseded       = "OVERLAY_SUPERSEDED"
)

type Input struct {
	TitleOverride      string
	ManualTags         []string
	Hidden             bool
	CustomCoverMediaID string
	Favorite           bool
	Progress           float64
}

type State struct {
	WorkID                      string
	TitleOverride               string
	ManualTags                  []string
	Hidden                      bool
	CustomCoverMediaID          string
	Favorite                    bool
	Progress                    float64
	FactWatermark               int64
	QueryWatermark              int64
	ProjectedWatermark          int64
	ProjectionStatus            string
	ProjectionJobID             string
	PublishedQueryPublicationID string
	IssueCode                   string
}

type PutResult struct {
	State
	StartJob bool
}

type Notifier interface {
	JobChanged(jobs.Job)
	PublicationPublished(catalog.Publication)
}

type nopNotifier struct{}

func (nopNotifier) JobChanged(jobs.Job)                      {}
func (nopNotifier) PublicationPublished(catalog.Publication) {}

type Service struct {
	context  context.Context
	control  *sql.DB
	jobs     *jobs.Store
	catalog  *catalog.Store
	clock    ports.Clock
	notifier Notifier
	wait     sync.WaitGroup

	// faultInjector 只供同包恢复测试设置；生产路径始终为 nil。
	faultInjector func(stage string) error
}

func New(ctx context.Context, control *sql.DB, jobStore *jobs.Store, catalogStore *catalog.Store, clock ports.Clock, notifier Notifier) (*Service, error) {
	if ctx == nil || control == nil || jobStore == nil || catalogStore == nil || clock == nil {
		return nil, fmt.Errorf("Overlay Service 缺少依赖")
	}
	if notifier == nil {
		notifier = nopNotifier{}
	}
	return &Service{context: ctx, control: control, jobs: jobStore, catalog: catalogStore, clock: clock, notifier: notifier}, nil
}

func (s *Service) Put(ctx context.Context, workID, createdBy string, input Input) (PutResult, error) {
	if _, err := domain.ParseID(domain.IDCanonicalWork, workID); err != nil || strings.TrimSpace(createdBy) == "" {
		return PutResult{}, fault.New(fault.CodeOverlayFactInvalid, false, nil)
	}
	normalized, err := normalizeInput(input)
	if err != nil {
		return PutResult{}, err
	}
	current, currentErr := s.catalog.Current(ctx)
	if currentErr != nil && !isCode(currentErr, fault.CodeNotFound) {
		return PutResult{}, currentErr
	}
	tx, err := s.control.BeginTx(ctx, nil)
	if err != nil {
		return PutResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM canonical_works WHERE work_id=?", workID).Scan(&exists); err != nil || exists != 1 {
		if err == nil {
			err = sql.ErrNoRows
		}
		return PutResult{}, fault.New(fault.CodeNotFound, false, err)
	}
	if normalized.CustomCoverMediaID != "" {
		var coverWork string
		if err := tx.QueryRowContext(ctx, "SELECT work_id FROM canonical_media WHERE media_id=?", normalized.CustomCoverMediaID).Scan(&coverWork); err != nil || coverWork != workID {
			return PutResult{}, fault.WithField(fault.CodeOverlayFactInvalid, "customCoverMediaId", err)
		}
	}
	previous, found, err := readStateTx(ctx, tx, workID)
	if err != nil {
		return PutResult{}, err
	}
	queryChanged := previous.TitleOverride != normalized.TitleOverride ||
		!equalStrings(previous.ManualTags, normalized.ManualTags) || previous.Hidden != normalized.Hidden ||
		previous.CustomCoverMediaID != normalized.CustomCoverMediaID || previous.ProjectionStatus == "failed" ||
		(previous.ProjectionStatus == "pending" && previous.ProjectionJobID == "")
	var watermark int64
	if err := tx.QueryRowContext(ctx, `UPDATE gallery_control_sequence SET watermark=watermark+1
WHERE singleton=1 RETURNING watermark`).Scan(&watermark); err != nil {
		return PutResult{}, fault.New(fault.CodeInternal, true, err)
	}
	projectionStatus, projectionJobID := previous.ProjectionStatus, previous.ProjectionJobID
	queryWatermark, projectedWatermark := previous.QueryWatermark, previous.ProjectedWatermark
	publishedID, issueCode := previous.PublishedQueryPublicationID, previous.IssueCode
	if !found {
		projectionStatus = "published"
	}
	startJob := false
	if queryChanged {
		projectionStatus, queryWatermark, publishedID, issueCode = "pending", watermark, "", ""
		projectionJobID = ""
		if currentErr == nil {
			enqueued, err := s.jobs.EnqueueOverlayProjectionTx(ctx, tx, createdBy, current.CatalogRevisionID, current.ID, watermark)
			if err != nil {
				return PutResult{}, err
			}
			projectionJobID, startJob = enqueued.JobID, enqueued.Created
		}
	}
	tagsJSON, _ := json.Marshal(normalized.ManualTags)
	var cover, jobID, publicationID, issue any
	if normalized.CustomCoverMediaID != "" {
		cover = normalized.CustomCoverMediaID
	}
	if projectionJobID != "" {
		jobID = projectionJobID
	}
	if publishedID != "" {
		publicationID = publishedID
	}
	if issueCode != "" {
		issue = issueCode
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO work_overlays
(work_id, title_override, manual_tags_json, hidden, custom_cover_media_id, favorite, progress,
 fact_watermark, query_watermark, projected_watermark, projection_status, projection_job_id,
 published_query_publication_id, issue_code, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(work_id) DO UPDATE SET
 title_override=excluded.title_override, manual_tags_json=excluded.manual_tags_json,
 hidden=excluded.hidden, custom_cover_media_id=excluded.custom_cover_media_id,
 favorite=excluded.favorite, progress=excluded.progress, fact_watermark=excluded.fact_watermark,
 query_watermark=excluded.query_watermark, projected_watermark=excluded.projected_watermark,
 projection_status=excluded.projection_status, projection_job_id=excluded.projection_job_id,
 published_query_publication_id=excluded.published_query_publication_id,
 issue_code=excluded.issue_code, updated_at=excluded.updated_at`,
		workID, normalized.TitleOverride, string(tagsJSON), boolInt(normalized.Hidden), cover,
		boolInt(normalized.Favorite), normalized.Progress, watermark, queryWatermark, projectedWatermark,
		projectionStatus, jobID, publicationID, issue, s.clock.Now().UTC().Unix())
	if err != nil {
		return PutResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return PutResult{}, fault.New(fault.CodeInternal, true, err)
	}
	state, err := s.Get(ctx, workID)
	if err != nil {
		return PutResult{}, err
	}
	if startJob {
		if queued, jobErr := s.jobs.Get(ctx, projectionJobID); jobErr == nil {
			s.notifier.JobChanged(queued)
		}
	}
	return PutResult{State: state, StartJob: startJob}, nil
}

func (s *Service) Get(ctx context.Context, workID string) (State, error) {
	if _, err := domain.ParseID(domain.IDCanonicalWork, workID); err != nil {
		return State{}, fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := s.control.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	state, found, err := readStateTx(ctx, tx, workID)
	if err != nil {
		return State{}, err
	}
	if !found {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM canonical_works WHERE work_id=?", workID).Scan(&count); err != nil || count != 1 {
			return State{}, fault.New(fault.CodeNotFound, false, err)
		}
		state = State{WorkID: workID, ManualTags: []string{}, ProjectionStatus: "published"}
	}
	if err := tx.Commit(); err != nil {
		return State{}, fault.New(fault.CodeInternal, true, err)
	}
	return state, nil
}

func (s *Service) Start(jobID string) {
	if jobID == "" {
		return
	}
	s.wait.Add(1)
	go func() {
		defer s.wait.Done()
		_ = s.Execute(s.context, jobID)
	}()
}

func (s *Service) Wait() { s.wait.Wait() }

func (s *Service) Execute(ctx context.Context, jobID string) error {
	job, err := s.jobs.StartStage(ctx, jobID, "projecting")
	if err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	for {
		job, err = s.jobs.Get(ctx, jobID)
		if err != nil {
			return err
		}
		current, err := s.catalog.Current(ctx)
		if err != nil {
			return s.fail(ctx, job, err)
		}
		if current.ControlWatermark >= job.TargetWatermark {
			return s.superseded(ctx, job, current)
		}
		if current.CatalogRevisionID != job.TargetCatalogID {
			job, err = s.jobs.RetargetOverlayProjection(ctx, job.ID, current.CatalogRevisionID, current.ID)
			if err != nil {
				return s.fail(ctx, job, err)
			}
			s.notifier.JobChanged(job)
			continue
		}
		candidate, err := s.catalog.BeginOverlayCandidate(ctx, job.ID, job.TargetCatalogID, job.TargetWatermark)
		if err != nil {
			if isCode(err, fault.CodeConflict) {
				continue
			}
			return s.fail(ctx, job, err)
		}
		facts, err := s.readFacts(ctx, job.TargetWatermark)
		if err == nil {
			err = s.catalog.ApplyOverlayFacts(ctx, candidate, facts)
		}
		if err == nil {
			err = s.catalog.ValidateOverlayCandidate(ctx, candidate)
		}
		if err == nil && s.faultInjector != nil {
			err = s.faultInjector("before_publish")
		}
		if err != nil {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "aborted")
			return s.fail(ctx, job, err)
		}
		latest, err := s.jobs.Get(ctx, job.ID)
		if err != nil {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "aborted")
			return err
		}
		if latest.Status != jobs.StatusRunning {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "superseded")
			return nil
		}
		if latest.TargetWatermark != candidate.ControlWatermark || latest.TargetCatalogID != candidate.CatalogRevisionID {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "superseded")
			job, err = s.jobs.Progress(ctx, job.ID, "reprojecting", 0, 0)
			if err != nil {
				return err
			}
			s.notifier.JobChanged(job)
			continue
		}
		job, err = s.jobs.BeginPublishing(ctx, job.ID)
		if err != nil {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "aborted")
			return err
		}
		s.notifier.JobChanged(job)
		if s.faultInjector != nil {
			if err := s.faultInjector("publishing"); err != nil {
				_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "aborted")
				return s.fail(ctx, job, err)
			}
		}
		latest, err = s.jobs.Get(ctx, job.ID)
		if err != nil {
			return err
		}
		if latest.TargetWatermark != candidate.ControlWatermark || latest.TargetCatalogID != candidate.CatalogRevisionID {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "superseded")
			job, err = s.jobs.ResumeOverlayProjection(ctx, job.ID)
			if err != nil {
				return err
			}
			s.notifier.JobChanged(job)
			continue
		}
		publication, err := s.catalog.PublishOverlay(ctx, candidate)
		if err != nil {
			_ = s.catalog.FinishOverlayCandidate(ctx, candidate, "superseded")
			if isCode(err, fault.CodeConflict) {
				job, resumeErr := s.jobs.ResumeOverlayProjection(ctx, job.ID)
				if resumeErr != nil {
					return resumeErr
				}
				s.notifier.JobChanged(job)
				continue
			}
			return s.fail(ctx, job, err)
		}
		if s.faultInjector != nil {
			if err := s.faultInjector("after_publication"); err != nil {
				return err
			}
		}
		s.notifier.PublicationPublished(publication)
		job, err = s.jobs.Complete(ctx, job.ID, publication.ID)
		if err != nil {
			return err
		}
		if err := s.markPublished(ctx, job.ID, publication.ControlWatermark, publication.ID); err != nil {
			return err
		}
		s.notifier.JobChanged(job)
		return nil
	}
}

func (s *Service) Reconcile(ctx context.Context) error {
	nonterminal, err := s.jobs.ListByStatuses(ctx, jobs.StatusQueued, jobs.StatusRunning, jobs.StatusPublishing)
	if err != nil {
		return err
	}
	for _, job := range nonterminal {
		if job.Type != "overlay_projection" {
			continue
		}
		publication, publicationErr := s.catalog.PublicationForJob(ctx, job.ID)
		if publicationErr == nil {
			if job.Status != jobs.StatusCompleted {
				job, err = s.jobs.RecoverCompleted(ctx, job.ID, publication.ID)
				if err != nil {
					return err
				}
			}
			if err := s.markPublished(ctx, job.ID, publication.ControlWatermark, publication.ID); err != nil {
				return err
			}
			s.notifier.JobChanged(job)
			continue
		}
		if !isCode(publicationErr, fault.CodeNotFound) {
			return publicationErr
		}
		if err := s.catalog.AbortOverlayCandidatesForJob(ctx, job.ID); err != nil {
			return err
		}
		if job.Status != jobs.StatusQueued {
			job, err = s.jobs.RequeueInterruptedOverlay(ctx, job.ID)
			if err != nil {
				return err
			}
			s.notifier.JobChanged(job)
		}
		s.Start(job.ID)
	}
	completed, err := s.jobs.ListByStatuses(ctx, jobs.StatusCompleted)
	if err != nil {
		return err
	}
	for _, job := range completed {
		if job.Type != "overlay_projection" {
			continue
		}
		if _, err := s.catalog.PublicationForJob(ctx, job.ID); isCode(err, fault.CodeNotFound) {
			repaired, repairErr := s.jobs.MarkNeedsRepair(ctx, job.ID, string(fault.CodeCatalogPublicationAbsent))
			if repairErr != nil {
				return repairErr
			}
			s.notifier.JobChanged(repaired)
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) readFacts(ctx context.Context, target int64) (map[string]catalog.OverlayFact, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT work_id, title_override, manual_tags_json, hidden, custom_cover_media_id
FROM work_overlays WHERE query_watermark<=? ORDER BY work_id`, target)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string]catalog.OverlayFact)
	for rows.Next() {
		var workID, title, tagsJSON string
		var hidden int
		var cover sql.NullString
		if err := rows.Scan(&workID, &title, &tagsJSON, &hidden, &cover); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, fault.New(fault.CodeInternal, false, err)
		}
		result[workID] = catalog.OverlayFact{TitleOverride: title, ManualTags: tags, Hidden: hidden != 0, CustomCoverMediaID: cover.String}
	}
	return result, rows.Err()
}

func (s *Service) superseded(ctx context.Context, job jobs.Job, publication catalog.Publication) error {
	job, err := s.jobs.CancelOverlayAsSuperseded(ctx, job.ID)
	if err != nil {
		return err
	}
	if err := s.markPublished(ctx, job.ID, job.TargetWatermark, publication.ID); err != nil {
		return err
	}
	s.notifier.JobChanged(job)
	return nil
}

func (s *Service) fail(ctx context.Context, job jobs.Job, cause error) error {
	failed, err := s.jobs.Fail(ctx, job.ID, IssueProjectionFailed)
	if err == nil {
		_, _ = s.control.ExecContext(ctx, `UPDATE work_overlays SET projection_status='failed',
issue_code=?, updated_at=? WHERE projection_job_id=? AND query_watermark<=? AND projection_status='pending'`,
			IssueProjectionFailed, s.clock.Now().UTC().Unix(), job.ID, job.TargetWatermark)
		s.notifier.JobChanged(failed)
	}
	return cause
}

func (s *Service) markPublished(ctx context.Context, jobID string, target int64, publicationID string) error {
	_, err := s.control.ExecContext(ctx, `UPDATE work_overlays SET projection_status='published',
projected_watermark=query_watermark, published_query_publication_id=?, issue_code=NULL, updated_at=?
WHERE projection_job_id=? AND query_watermark<=? AND projection_status='pending'`,
		publicationID, s.clock.Now().UTC().Unix(), jobID, target)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func readStateTx(ctx context.Context, tx *sql.Tx, workID string) (State, bool, error) {
	var state State
	var tagsJSON string
	var hidden, favorite int
	var cover, jobID, publicationID, issue sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT work_id, title_override, manual_tags_json, hidden,
custom_cover_media_id, favorite, progress, fact_watermark, query_watermark, projected_watermark,
projection_status, projection_job_id, published_query_publication_id, issue_code
FROM work_overlays WHERE work_id=?`, workID).Scan(&state.WorkID, &state.TitleOverride, &tagsJSON,
		&hidden, &cover, &favorite, &state.Progress, &state.FactWatermark, &state.QueryWatermark,
		&state.ProjectedWatermark, &state.ProjectionStatus, &jobID, &publicationID, &issue)
	if errors.Is(err, sql.ErrNoRows) {
		return State{WorkID: workID, ManualTags: []string{}, ProjectionStatus: "published"}, false, nil
	}
	if err != nil {
		return State{}, false, fault.New(fault.CodeInternal, true, err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &state.ManualTags); err != nil {
		return State{}, false, fault.New(fault.CodeInternal, false, err)
	}
	state.Hidden, state.Favorite = hidden != 0, favorite != 0
	state.CustomCoverMediaID, state.ProjectionJobID = cover.String, jobID.String
	state.PublishedQueryPublicationID, state.IssueCode = publicationID.String, issue.String
	return state, true, nil
}

func normalizeInput(input Input) (Input, error) {
	input.TitleOverride = strings.TrimSpace(norm.NFC.String(input.TitleOverride))
	if len([]rune(input.TitleOverride)) > 4096 || input.Progress < 0 || input.Progress > 1 {
		return Input{}, fault.New(fault.CodeOverlayFactInvalid, false, nil)
	}
	for _, value := range input.TitleOverride {
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return Input{}, fault.WithField(fault.CodeOverlayFactInvalid, "titleOverride", nil)
		}
	}
	if input.CustomCoverMediaID != "" {
		if _, err := domain.ParseID(domain.IDCanonicalMedia, input.CustomCoverMediaID); err != nil {
			return Input{}, fault.WithField(fault.CodeOverlayFactInvalid, "customCoverMediaId", err)
		}
	}
	if len(input.ManualTags) > 200 {
		return Input{}, fault.WithField(fault.CodeOverlayFactInvalid, "manualTags", nil)
	}
	seen := make(map[string]struct{}, len(input.ManualTags))
	tags := make([]string, 0, len(input.ManualTags))
	for _, raw := range input.ManualTags {
		value := strings.TrimSpace(norm.NFC.String(raw))
		if value == "" || len([]rune(value)) > 512 {
			return Input{}, fault.WithField(fault.CodeOverlayFactInvalid, "manualTags", nil)
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			tags = append(tags, value)
		}
	}
	sort.Strings(tags)
	input.ManualTags = tags
	return input, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isCode(err error, code fault.Code) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == code
}
