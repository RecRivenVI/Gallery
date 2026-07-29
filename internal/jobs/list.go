package jobs

import (
	"context"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

// IsPublicStatus 只接受 HTTP Job DTO 能实际表达的状态。cancelling 与 superseded
// 是持久原始状态和附加事实组合出的公开状态，也必须在这里显式登记。
func IsPublicStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusPublishing, StatusCompleted, StatusFailed,
		StatusCancelled, StatusNeedsRepair, StatusCancelling, StatusSuperseded:
		return true
	default:
		return false
	}
}

// ListPage 以创建时间和 Job ID 的不可变组合执行 newest-first keyset 查询。
// 它只裁剪状态和候选页，不承担授权；调用方必须在形成响应前重新授权每一行。
func (s *Store) ListPage(ctx context.Context, request ListPageRequest) ([]Job, error) {
	if request.Limit < 1 || request.Limit > 1000 {
		return nil, fault.WithField(fault.CodeValidation, "limit", nil)
	}
	if request.Status != "" && !IsPublicStatus(request.Status) {
		return nil, fault.WithField(fault.CodeValidation, "status", nil)
	}
	if request.Before != nil {
		if request.Before.CreatedAt.IsZero() || request.Before.CreatedAt.Nanosecond() != 0 {
			return nil, fault.WithField(fault.CodeValidation, "cursor", nil)
		}
		if _, err := domain.ParseID(domain.IDJob, request.Before.JobID); err != nil {
			return nil, fault.WithField(fault.CodeValidation, "cursor", err)
		}
	}

	predicate, args := jobPageStatusPredicate(request.Status)
	if request.Before != nil {
		predicate += " AND (created_at < ? OR (created_at = ? AND job_id < ?))"
		unix := request.Before.CreatedAt.Unix()
		args = append(args, unix, unix, request.Before.JobID)
	}
	args = append(args, request.Limit)
	rows, err := s.db.QueryContext(ctx, jobSelect+" WHERE "+predicate+" ORDER BY created_at DESC, job_id DESC LIMIT ?", args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]Job, 0, request.Limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func jobPageStatusPredicate(status Status) (string, []any) {
	switch status {
	case "":
		return "1=1", nil
	case StatusRunning, StatusPublishing:
		return "status = ? AND cancel_requested = 0", []any{status}
	case StatusCancelling:
		return "status IN ('running', 'publishing') AND cancel_requested = 1", nil
	case StatusCancelled:
		return "status = 'cancelled' AND stage <> 'superseded'", nil
	case StatusSuperseded:
		return "status = 'cancelled' AND stage = 'superseded'", nil
	default:
		return "status = ?", []any{status}
	}
}
