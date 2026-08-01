// Package maintenanceperf 经公开 HTTP 契约驱动真实 galleryd 的维护性能矩阵。
// 它不导入 internal/maintenance，不读 SQLite 内容；AppDirs 只用于统计非敏感的
// 文件总字节，从而核对空间估算和 VACUUM 临时峰值。
package maintenanceperf

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	api "github.com/RecRivenVI/gallery/api"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

const (
	OperationCatalogGC         = "catalog_gc"
	OperationCatalogCheckpoint = "catalog_checkpoint"
	OperationCatalogVacuum     = "catalog_vacuum"
)

type Options struct {
	DataDir                string
	GCRuns                 int
	CheckpointRuns         int
	VacuumRuns             int
	QueryInterval          time.Duration
	QueryTimeout           time.Duration
	OperationTimeout       time.Duration
	PublicationFingerprint string
	HistoricalPublication  bool
}

type operationPlan struct {
	name string
	runs int
}

// Run 执行固定顺序的 GC/checkpoint/VACUUM；调用方可把某类 runs 设为 0 跳过，
// 但至少要有一类大于 0。维护期间的固定 publication 读取是串行单请求采样，不制造无界负载。
func Run(rep *report.Report, sess *environment.Session, publicationID string, opts Options) error {
	if err := validateOptions(rep, sess, publicationID, opts); err != nil {
		return err
	}
	plans := []operationPlan{
		{name: OperationCatalogGC, runs: opts.GCRuns},
		{name: OperationCatalogCheckpoint, runs: opts.CheckpointRuns},
		{name: OperationCatalogVacuum, runs: opts.VacuumRuns},
	}
	matrix := &report.MaintenanceMatrix{
		Fingerprint: fingerprint(publicationID, opts), PublicationFingerprint: opts.PublicationFingerprint,
		HistoricalPublication: opts.HistoricalPublication,
		PercentileMethod:      "nearest-rank", QueryIntervalMs: opts.QueryInterval.Milliseconds(),
		QueryTimeoutMs: opts.QueryTimeout.Milliseconds(), OperationTimeoutMs: opts.OperationTimeout.Milliseconds(),
	}
	rep.MaintenanceMatrix = matrix
	rep.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)

	for _, plan := range plans {
		if plan.runs == 0 {
			continue
		}
		result := report.MaintenanceOperationResult{Operation: plan.name, PlannedRuns: plan.runs}
		for run := 1; run <= plan.runs; run++ {
			sample, err := runOnce(sess, publicationID, plan.name, run, opts)
			result.Runs = append(result.Runs, sample)
			if err != nil {
				result.FailedRuns++
				matrix.Operations = append(matrix.Operations, summarize(result))
				rep.Add(fmt.Sprintf("maintenance/%s-run%d", plan.name, run), false, err.Error())
				rep.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
				return err
			}
			result.CompletedRuns++
			addRunFindings(rep, plan.name, sample, opts.QueryInterval)
		}
		result = summarize(result)
		matrix.Operations = append(matrix.Operations, result)
		rep.Add(fmt.Sprintf("maintenance/%s-runs-complete", plan.name), result.CompletedRuns == result.PlannedRuns,
			fmt.Sprintf("completed=%d planned=%d failed=%d", result.CompletedRuns, result.PlannedRuns, result.FailedRuns))
	}

	rep.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	rep.Add("maintenance/matrix-completed", len(matrix.Operations) > 0, "")
	rep.Limitations = append(rep.Limitations,
		"AppDirs 空间峰值按 queryInterval 周期采样，短于采样间隔的临时文件可能不会进入 sampled peak；该数字不是文件系统块级峰值。")
	if !opts.HistoricalPublication {
		rep.Limitations = append(rep.Limitations,
			"本次固定 publication 是 manifest 当前 active publication；它验证维护期间读取与 lease 写入，不替代历史 publication 跨维护可读性门禁。")
	}
	return nil
}

func validateOptions(rep *report.Report, sess *environment.Session, publicationID string, opts Options) error {
	if rep == nil || sess == nil || sess.Client == nil || publicationID == "" || opts.DataDir == "" {
		return fmt.Errorf("maintenance perf 缺少报告、Session、publication 或 AppDirs data 目录")
	}
	if opts.GCRuns < 0 || opts.CheckpointRuns < 0 || opts.VacuumRuns < 0 || opts.GCRuns+opts.CheckpointRuns+opts.VacuumRuns == 0 {
		return fmt.Errorf("maintenance perf runs 必须非负且至少有一类大于 0")
	}
	if opts.QueryInterval <= 0 || opts.QueryTimeout <= 0 || opts.OperationTimeout <= 0 {
		return fmt.Errorf("maintenance perf 的采样、查询和操作 timeout 必须为正")
	}
	info, err := os.Stat(opts.DataDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("maintenance perf 的 AppDirs data 目录不可用")
	}
	return nil
}

func fingerprint(publicationID string, opts Options) string {
	payload := fmt.Sprintf("maintenance-reference-v1\x00%s\x00%s\x00%t\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d",
		publicationID, opts.PublicationFingerprint, opts.HistoricalPublication, opts.GCRuns, opts.CheckpointRuns, opts.VacuumRuns,
		opts.QueryInterval.Nanoseconds(), opts.QueryTimeout.Nanoseconds(), opts.OperationTimeout.Nanoseconds())
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum)
}

type browseSample struct {
	started, finished time.Time
	duration          time.Duration
	success           bool
	timedOut          bool
}

func runOnce(sess *environment.Session, publicationID, operation string, run int, opts Options) (report.MaintenanceRunSample, error) {
	sample := report.MaintenanceRunSample{Run: run}
	before, _, _ := browseSnapshot(sess, publicationID, opts.QueryTimeout)
	sample.PublicationReadableBefore = before
	if !before {
		return sample, fmt.Errorf("%s run %d 操作前固定 publication 不可读", operation, run)
	}
	bytesBefore, err := dataBytes(opts.DataDir)
	if err != nil {
		return sample, fmt.Errorf("%s run %d 统计操作前 AppDirs 字节失败", operation, run)
	}
	sample.AppDataBytesBefore = bytesBefore

	watchCtx, stopWatch := context.WithCancel(context.Background())
	var browseMu sync.Mutex
	browseSamples := make([]browseSample, 0, 64)
	var bytesMu sync.Mutex
	peakBytes := bytesBefore
	var watchers sync.WaitGroup
	watchers.Add(2)
	go func() {
		defer watchers.Done()
		for {
			started := time.Now().UTC()
			success, timedOut, duration := browseSnapshot(sess, publicationID, opts.QueryTimeout)
			finished := time.Now().UTC()
			browseMu.Lock()
			browseSamples = append(browseSamples, browseSample{started: started, finished: finished, duration: duration, success: success, timedOut: timedOut})
			browseMu.Unlock()
			if !waitInterval(watchCtx, opts.QueryInterval) {
				return
			}
		}
	}()
	go func() {
		defer watchers.Done()
		for {
			if current, sizeErr := dataBytes(opts.DataDir); sizeErr == nil {
				bytesMu.Lock()
				if current > peakBytes {
					peakBytes = current
				}
				bytesMu.Unlock()
			}
			if !waitInterval(watchCtx, opts.QueryInterval) {
				return
			}
		}
	}()

	createdAt := time.Now().UTC()
	created, err := createOperation(sess, operation, opts.QueryTimeout)
	if err != nil {
		stopWatch()
		watchers.Wait()
		return sample, fmt.Errorf("%s run %d 创建维护 Job 失败: %v", operation, run, err)
	}
	sample.RequiredBytes = created.SpaceEstimate.RequiredBytes
	sample.AvailableBytes = created.SpaceEstimate.AvailableBytes
	sample.SpaceSufficient = created.SpaceEstimate.Sufficient
	sample.SpaceConservative = created.SpaceEstimate.Conservative

	job, err := waitTerminal(sess, created.Job.Id, opts.OperationTimeout, opts.QueryTimeout)
	terminalObservedAt := time.Now().UTC()
	stopWatch()
	watchers.Wait()
	afterBytes, sizeErr := dataBytes(opts.DataDir)
	if sizeErr != nil {
		return sample, fmt.Errorf("%s run %d 统计操作后 AppDirs 字节失败", operation, run)
	}
	bytesMu.Lock()
	if afterBytes > peakBytes {
		// 最终状态本身也是一次明确采样；watcher 已停止后产生的 Job 终态 WAL 不能被
		// 排除在 sampled peak 之外，否则 after 可能大于所谓 peak。
		peakBytes = afterBytes
	}
	sample.AppDataBytesPeakSampled = peakBytes
	bytesMu.Unlock()
	sample.AppDataBytesAfter = afterBytes
	if err != nil {
		return sample, fmt.Errorf("%s run %d 等待维护 Job 失败: %v", operation, run, err)
	}

	// Job Store 的持久时间戳当前只有秒级精度，不能用于亚秒维护性能；正式墙钟固定为
	// 客户端发出创建请求前到公共 Job API 首次观察终态，包含排队和轮询，是用户实际
	// 可见的完整维护窗口。publication 采样也按同一窗口筛选，不能用粗粒度 server 时间把
	// 操作前/后的请求误算成维护期间读取。
	startedAt, finishedAt := createdAt, terminalObservedAt
	sample.DurationMs = float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
	sample.FinalStatus = string(job.Status)
	if job.IssueCode != nil {
		sample.FinalIssueCode = *job.IssueCode
	}
	sample.FinalProgressCurrent = job.Progress.Current
	sample.FinalProgressTotal = job.Progress.Total
	if job.Progress.Estimated != nil {
		sample.FinalProgressEstimated = *job.Progress.Estimated
	}

	browseMu.Lock()
	overlapped := overlappingBrowseSamples(browseSamples, startedAt, finishedAt)
	browseMu.Unlock()
	fillBrowseSummary(&sample, overlapped)
	after, _, _ := browseSnapshot(sess, publicationID, opts.QueryTimeout)
	sample.PublicationReadableAfter = after
	if job.Status != api.JobStatusCompleted {
		return sample, fmt.Errorf("%s run %d 维护 Job 终态不是 completed: status=%s", operation, run, job.Status)
	}
	if !after {
		return sample, fmt.Errorf("%s run %d 操作后固定 publication 不可读", operation, run)
	}
	return sample, nil
}

func waitInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func browseSnapshot(sess *environment.Session, publicationID string, timeout time.Duration) (bool, bool, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	limit, omitTotal := 20, true
	started := time.Now()
	response, err := sess.Client.ListWorksWithResponse(ctx, &api.ListWorksParams{
		Limit: &limit, OmitTotal: &omitTotal, QueryPublicationId: &publicationID,
	}, sess.SameOrigin)
	duration := time.Since(started)
	if err != nil {
		return false, errors.Is(ctx.Err(), context.DeadlineExceeded), duration
	}
	return response != nil && response.JSON200 != nil && response.JSON200.QueryPublicationId == publicationID, false, duration
}

func createOperation(sess *environment.Session, operation string, timeout time.Duration) (api.MaintenanceJobResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	switch operation {
	case OperationCatalogGC:
		dryRun := false
		retention := int64(0)
		response, err := sess.Client.CreateCatalogGCJobWithResponse(ctx,
			&api.CreateCatalogGCJobParams{XGalleryCSRF: sess.CSRF},
			api.MaintenanceGCRequest{DryRun: &dryRun, RetentionSeconds: &retention}, sess.SameOrigin)
		if err != nil || response == nil || response.JSON202 == nil {
			return api.MaintenanceJobResponse{}, fmt.Errorf("status=%d err=%v", environment.StatusOf(response), err)
		}
		return *response.JSON202, nil
	case OperationCatalogCheckpoint:
		response, err := sess.Client.CreateCatalogCheckpointJobWithResponse(ctx,
			&api.CreateCatalogCheckpointJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
		if err != nil || response == nil || response.JSON202 == nil {
			return api.MaintenanceJobResponse{}, fmt.Errorf("status=%d err=%v", environment.StatusOf(response), err)
		}
		return *response.JSON202, nil
	case OperationCatalogVacuum:
		response, err := sess.Client.CreateCatalogVacuumJobWithResponse(ctx,
			&api.CreateCatalogVacuumJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
		if err != nil || response == nil || response.JSON202 == nil {
			return api.MaintenanceJobResponse{}, fmt.Errorf("status=%d err=%v", environment.StatusOf(response), err)
		}
		return *response.JSON202, nil
	default:
		return api.MaintenanceJobResponse{}, fmt.Errorf("未知维护操作 %q", operation)
	}
}

func waitTerminal(sess *environment.Session, jobID string, operationTimeout, requestTimeout time.Duration) (api.Job, error) {
	deadline := time.Now().Add(operationTimeout)
	var lastTransient error
	for {
		if time.Now().After(deadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			_, _ = sess.Client.CancelJobWithResponse(cancelCtx, jobID, &api.CancelJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
			cancel()
			return api.Job{}, fmt.Errorf("维护操作超过 %s，已请求取消（最后一次瞬态观察错误: %v）", operationTimeout, lastTransient)
		}
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		response, err := sess.Client.GetJobWithResponse(ctx, jobID, sess.SameOrigin)
		cancel()
		status := environment.StatusOf(response)
		if err != nil || response == nil || (response.JSON200 == nil && status >= 500) {
			// 大型 SQLite 维护可能让同进程 HTTP 调度出现一次请求级超时；只要总体
			// operation budget 未耗尽，就继续以公共 API 观察。单次网络抖动不能让
			// 执行器杀掉仍在持久 heartbeat 的真实 Job。
			lastTransient = fmt.Errorf("status=%d err=%v", status, err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if response.JSON200 == nil {
			return api.Job{}, fmt.Errorf("读取维护 Job 被拒绝: status=%d", status)
		}
		job := *response.JSON200
		switch job.Status {
		case api.JobStatusCompleted, api.JobStatusFailed, api.JobStatusCancelled:
			return job, nil
		}
		// 维护操作按秒到分钟计；250 ms 足以观察用户可见终态，同时避免真实
		// 500k 运行每秒写入数十条 Job GET 日志并制造无意义的 control.db WAL。
		time.Sleep(250 * time.Millisecond)
	}
}

func overlappingBrowseSamples(samples []browseSample, startedAt, finishedAt time.Time) []browseSample {
	result := make([]browseSample, 0, len(samples))
	for _, sample := range samples {
		if sample.started.Before(finishedAt) && sample.finished.After(startedAt) {
			result = append(result, sample)
		}
	}
	return result
}

func fillBrowseSummary(sample *report.MaintenanceRunSample, observations []browseSample) {
	sample.DuringObserved = len(observations) > 0
	sample.DuringAttempts = len(observations)
	durations := make([]float64, 0, len(observations))
	for _, observation := range observations {
		if observation.success {
			sample.DuringSuccessful++
			durations = append(durations, float64(observation.duration)/float64(time.Millisecond))
		} else {
			sample.DuringFailed++
			if observation.timedOut {
				sample.DuringTimedOut++
			}
		}
	}
	if len(durations) == 0 {
		return
	}
	sort.Float64s(durations)
	sample.DuringP50Ms = percentile(durations, 50)
	sample.DuringP95Ms = percentile(durations, 95)
	sample.DuringMaxMs = durations[len(durations)-1]
}

func addRunFindings(rep *report.Report, operation string, sample report.MaintenanceRunSample, interval time.Duration) {
	prefix := fmt.Sprintf("maintenance/%s-run%d", operation, sample.Run)
	rep.Add(prefix+"-completed", sample.FinalStatus == string(api.JobStatusCompleted), "")
	rep.Add(prefix+"-space-preflight", sample.SpaceSufficient && sample.SpaceConservative,
		fmt.Sprintf("sufficient=%t conservative=%t requiredBytes=%d availableBytes=%d", sample.SpaceSufficient, sample.SpaceConservative, sample.RequiredBytes, sample.AvailableBytes))
	rep.Add(prefix+"-final-progress", sample.FinalProgressCurrent == 2 && sample.FinalProgressTotal == 2 && sample.FinalProgressEstimated,
		fmt.Sprintf("current=%d total=%d estimated=%t", sample.FinalProgressCurrent, sample.FinalProgressTotal, sample.FinalProgressEstimated))
	rep.Add(prefix+"-publication-before-after", sample.PublicationReadableBefore && sample.PublicationReadableAfter, "")
	if sample.DurationMs >= float64((2*interval)/time.Millisecond) {
		rep.Add(prefix+"-publication-during-observed", sample.DuringObserved, fmt.Sprintf("attempts=%d", sample.DuringAttempts))
	}
	if sample.DuringObserved {
		rep.Add(prefix+"-publication-during-readable", sample.DuringFailed == 0 && sample.DuringSuccessful == sample.DuringAttempts,
			fmt.Sprintf("attempts=%d successful=%d failed=%d timedOut=%d p95Ms=%.3f maxMs=%.3f",
				sample.DuringAttempts, sample.DuringSuccessful, sample.DuringFailed, sample.DuringTimedOut, sample.DuringP95Ms, sample.DuringMaxMs))
	}
	rep.Add(prefix+"-sampled-space-identity", sample.AppDataBytesPeakSampled >= sample.AppDataBytesBefore && sample.AppDataBytesPeakSampled >= sample.AppDataBytesAfter,
		fmt.Sprintf("before=%d sampledPeak=%d after=%d", sample.AppDataBytesBefore, sample.AppDataBytesPeakSampled, sample.AppDataBytesAfter))
}

func summarize(result report.MaintenanceOperationResult) report.MaintenanceOperationResult {
	durations := make([]float64, 0, len(result.Runs))
	for _, run := range result.Runs {
		if run.FinalStatus == string(api.JobStatusCompleted) {
			durations = append(durations, run.DurationMs)
		}
	}
	if len(durations) == 0 {
		return result
	}
	sort.Float64s(durations)
	result.DurationP50Ms = percentile(durations, 50)
	result.DurationP95Ms = percentile(durations, 95)
	result.DurationMinMs = durations[0]
	result.DurationMaxMs = durations[len(durations)-1]
	return result
}

func percentile(sorted []float64, percent int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(float64(percent*len(sorted)) / 100.0))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func dataBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
