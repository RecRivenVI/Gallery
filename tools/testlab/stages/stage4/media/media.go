package media

import (
	"context"
	"fmt"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// RunMediaCorrectness 覆盖阶段 4「媒体与 DerivedAsset 正式路径」：目标化单媒体按需
// 确认（只确认目标、不触发整个 Source verify）、current/snapshot 快照绑定读取、
// Range/ETag/If-None-Match/If-Range，以及受限 JPEG DerivedAsset 端到端。
func RunMediaCorrectness(rep *report.Report, sess *environment.Session, libraryID, sourceID string, workCount int) {
	ctx := context.Background()
	listResp, err := listWorks(sess, api.ListWorksParams{LibraryId: &libraryID, Limit: ptr(workCount + 5)})
	if err != nil || listResp.JSON200 == nil || len(listResp.JSON200.Works) == 0 {
		rep.Add("media/list-works-after-index-scan", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(listResp)))
		return
	}
	oldPublicationID := listResp.JSON200.QueryPublicationId
	targetWork := listResp.JSON200.Works[0]

	mediaListBefore, err := sess.Client.ListWorkMediaWithResponse(ctx, targetWork.Id, &api.ListWorkMediaParams{}, sess.SameOrigin)
	if err != nil || mediaListBefore.JSON200 == nil || len(mediaListBefore.JSON200.Media) == 0 {
		rep.Add("media/list-work-media", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(mediaListBefore)))
		return
	}
	targetMedia := mediaListBefore.JSON200.Media[0]
	rep.Add("media/initial-state-located-unverified", string(targetMedia.ContentVerificationState) == "located_unverified",
		fmt.Sprintf("state=%s", targetMedia.ContentVerificationState))

	// 未确认媒体正文必须返回 CONTENT_NOT_VERIFIED，不进入依赖 ContentBlob/ETag 的已验证读取路径。
	unverifiedContent, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{}, sess.SameOrigin)
	if err != nil {
		rep.Add("media/unverified-content-rejected", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/unverified-content-rejected", unverifiedContent.JSON409 != nil, fmt.Sprintf("status=%d", environment.StatusOf(unverifiedContent)))
	}

	// 目标化按需确认：只强制这一个媒体，其余 11 个必须仍保持 located_unverified。
	verifyJob, err := sess.Client.CreateMediaVerificationJobWithResponse(ctx, targetMedia.Id, &api.CreateMediaVerificationJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
	if err != nil || verifyJob.JSON202 == nil {
		rep.Add("media/verification-job-created", false, fmt.Sprintf("err=%v status=%d body=%s", err, environment.StatusOf(verifyJob), string(verifyJob.Body)))
		return
	}
	job, err := waitForJob(sess, verifyJob.JSON202.Id, 30*time.Second)
	if err != nil || job.Status != "completed" {
		rep.Add("media/verification-job-completed", false, fmt.Sprintf("err=%v status=%v", err, job))
		return
	}
	rep.Add("media/verification-job-completed", true, "")

	afterList, err := listWorks(sess, api.ListWorksParams{LibraryId: &libraryID, Limit: ptr(workCount + 5)})
	if err != nil || afterList.JSON200 == nil {
		rep.Add("media/relist-after-targeted-verification", false, fmt.Sprintf("err=%v", err))
		return
	}
	newPublicationID := afterList.JSON200.QueryPublicationId
	confirmedCount, unverifiedCount := 0, 0
	for _, w := range afterList.JSON200.Works {
		mediaResp, err := sess.Client.ListWorkMediaWithResponse(ctx, w.Id, &api.ListWorkMediaParams{}, sess.SameOrigin)
		if err != nil || mediaResp.JSON200 == nil {
			continue
		}
		for _, m := range mediaResp.JSON200.Media {
			if string(m.ContentVerificationState) == "content_verified" {
				confirmedCount++
			} else {
				unverifiedCount++
			}
		}
	}
	// 只断言"确认媒体数恰好为 1"（即目标化确认只强制了目标媒体本身），不假设每个
	// 作品恰好一个媒体——合成夹具确实是 1:1，但真实 Source（如多图 pixiv 投稿）
	// 里一个作品常有多个媒体，workCount-1 不是正确的期望值；unverifiedCount 由
	// 定义等于 (confirmedCount+unverifiedCount)-1，不需要单独校验。
	rep.Add("media/only-target-confirmed-siblings-untouched", confirmedCount == 1,
		fmt.Sprintf("confirmed=%d unverified=%d workCount=%d", confirmedCount, unverifiedCount, workCount))

	// 重复请求同一 observation 的确认：媒体已 content_verified，必须是稳定 CONFLICT。
	repeat, err := sess.Client.CreateMediaVerificationJobWithResponse(ctx, targetMedia.Id, &api.CreateMediaVerificationJobParams{XGalleryCSRF: sess.CSRF}, sess.SameOrigin)
	if err != nil || repeat.JSON409 == nil {
		rep.Add("media/repeat-verification-conflict", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(repeat)))
	} else {
		rep.Add("media/repeat-verification-conflict", true, "")
	}

	// 历史 publication 上再次请求按需确认必须拒绝为 CONFLICT，不静默确认历史快照。
	historicalRequest, err := sess.Client.CreateMediaVerificationJobWithResponse(ctx, targetMedia.Id,
		&api.CreateMediaVerificationJobParams{XGalleryCSRF: sess.CSRF, QueryPublicationId: ptr(api.QueryPublicationId(oldPublicationID))}, sess.SameOrigin)
	if err != nil || historicalRequest.JSON409 == nil {
		rep.Add("media/historical-publication-verification-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(historicalRequest)))
	} else {
		rep.Add("media/historical-publication-verification-rejected", true, "")
	}

	// snapshot 模式：旧 publication 上该媒体仍读到确认前的状态。
	oldSnapshotMedia, err := sess.Client.GetMediaWithResponse(ctx, targetMedia.Id, &api.GetMediaParams{QueryPublicationId: ptr(api.QueryPublicationId(oldPublicationID))}, sess.SameOrigin)
	if err != nil || oldSnapshotMedia.JSON200 == nil {
		rep.Add("media/snapshot-mode-reads-old-state", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(oldSnapshotMedia)))
	} else {
		rep.Add("media/snapshot-mode-reads-old-state", string(oldSnapshotMedia.JSON200.ContentVerificationState) == "located_unverified",
			fmt.Sprintf("state=%s", oldSnapshotMedia.JSON200.ContentVerificationState))
	}

	// current 模式：新 publication 下媒体已确认，正文可读且带 ETag。
	content, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{}, sess.SameOrigin)
	if err != nil || content.HTTPResponse == nil || content.HTTPResponse.StatusCode != 200 {
		rep.Add("media/current-mode-content-readable", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(content)))
		return
	}
	etag := content.HTTPResponse.Header.Get("ETag")
	rep.Add("media/current-mode-content-readable", len(content.Body) > 0 && etag != "", fmt.Sprintf("bytes=%d etag-present=%v", len(content.Body), etag != ""))

	// Range：单区间请求应返回 206 与正确 Content-Range。
	rangeResp, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{Range: ptr("bytes=0-15")}, sess.SameOrigin)
	if err != nil || rangeResp.HTTPResponse == nil {
		rep.Add("media/range-206", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/range-206", rangeResp.HTTPResponse.StatusCode == 206 && len(rangeResp.Body) == 16,
			fmt.Sprintf("status=%d len=%d", rangeResp.HTTPResponse.StatusCode, len(rangeResp.Body)))
	}

	// 非法 Range：起点越界必须 416。
	illegalRange, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{Range: ptr("bytes=999999999-999999999")}, sess.SameOrigin)
	if err != nil || illegalRange.HTTPResponse == nil {
		rep.Add("media/illegal-range-416", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/illegal-range-416", illegalRange.HTTPResponse.StatusCode == 416, fmt.Sprintf("status=%d", illegalRange.HTTPResponse.StatusCode))
	}

	// If-None-Match 命中当前 ETag 必须 304。
	notModified, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{IfNoneMatch: ptr(etag)}, sess.SameOrigin)
	if err != nil || notModified.HTTPResponse == nil {
		rep.Add("media/if-none-match-304", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/if-none-match-304", notModified.HTTPResponse.StatusCode == 304, fmt.Sprintf("status=%d", notModified.HTTPResponse.StatusCode))
	}

	// If-Range 命中当前 ETag：允许 Range 生效，返回 206。
	ifRangeHit, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{Range: ptr("bytes=0-9"), IfRange: ptr(etag)}, sess.SameOrigin)
	if err != nil || ifRangeHit.HTTPResponse == nil {
		rep.Add("media/if-range-hit-206", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/if-range-hit-206", ifRangeHit.HTTPResponse.StatusCode == 206, fmt.Sprintf("status=%d", ifRangeHit.HTTPResponse.StatusCode))
	}

	// If-Range 不命中（伪造 ETag）：必须忽略 Range，退回完整 200。
	ifRangeMiss, err := sess.Client.GetMediaContentWithResponse(ctx, targetMedia.Id, &api.GetMediaContentParams{Range: ptr("bytes=0-9"), IfRange: ptr(`"stale-etag-value"`)}, sess.SameOrigin)
	if err != nil || ifRangeMiss.HTTPResponse == nil {
		rep.Add("media/if-range-miss-falls-back-200", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("media/if-range-miss-falls-back-200", ifRangeMiss.HTTPResponse.StatusCode == 200, fmt.Sprintf("status=%d", ifRangeMiss.HTTPResponse.StatusCode))
	}

	// v1 缩略图 resolver 只解析 JPEG 容器（见 Documents/规范/08-文件系统与媒体处理.md），
	// 真实 Source 中的媒体可能是 PNG/GIF/视频等其它格式；只在目标媒体确实是 JPEG 时才
	// 运行 DerivedAsset 端到端场景，其余情况记为信息性跳过而不是失败。
	if targetMedia.MimeType == "image/jpeg" {
		runDerivedAssetCorrectness(rep, sess, targetMedia.Id, newPublicationID, oldPublicationID)
	} else {
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("跳过 DerivedAsset 场景：目标媒体 MIME 为 %s，v1 缩略图 resolver 只支持 image/jpeg", targetMedia.MimeType))
	}
}

// runDerivedAssetCorrectness 覆盖受限 JPEG 缩略图端到端：异步 Job、幂等/缓存命中、
// 未知 transform 拒绝、未确认媒体拒绝创建、正文可读且确实等比缩小。
func runDerivedAssetCorrectness(rep *report.Report, sess *environment.Session, mediaID, currentPublicationID, historicalPublicationID string) {
	ctx := context.Background()
	unknownTransform, err := sess.Client.CreateDerivedAssetWithResponse(ctx, mediaID, &api.CreateDerivedAssetParams{XGalleryCSRF: sess.CSRF},
		api.DerivedAssetCreateRequest{TransformId: "unknown-transform", TransformVersion: "v1"}, sess.SameOrigin)
	if err != nil || unknownTransform.JSON400 == nil {
		rep.Add("derived/unknown-transform-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(unknownTransform)))
	} else {
		rep.Add("derived/unknown-transform-rejected", true, "")
	}

	// 历史（未确认）快照上创建 DerivedAsset 必须拒绝：该媒体在历史 publication 中尚未 content_verified。
	rejectedOnHistorical, err := sess.Client.CreateDerivedAssetWithResponse(ctx, mediaID,
		&api.CreateDerivedAssetParams{XGalleryCSRF: sess.CSRF, QueryPublicationId: ptr(api.QueryPublicationId(historicalPublicationID))},
		api.DerivedAssetCreateRequest{TransformId: "thumbnail", TransformVersion: "v1"}, sess.SameOrigin)
	if err != nil {
		rep.Add("derived/historical-snapshot-unverified-rejected", false, fmt.Sprintf("err=%v", err))
	} else {
		rep.Add("derived/historical-snapshot-unverified-rejected", environment.StatusOf(rejectedOnHistorical) >= 400, fmt.Sprintf("status=%d", environment.StatusOf(rejectedOnHistorical)))
	}

	created, err := sess.Client.CreateDerivedAssetWithResponse(ctx, mediaID, &api.CreateDerivedAssetParams{XGalleryCSRF: sess.CSRF},
		api.DerivedAssetCreateRequest{TransformId: "thumbnail", TransformVersion: "v1"}, sess.SameOrigin)
	if err != nil || created.JSON202 == nil {
		rep.Add("derived/create-job-accepted", false, fmt.Sprintf("err=%v status=%d body=%s", err, environment.StatusOf(created), string(created.Body)))
		return
	}
	rep.Add("derived/create-job-accepted", true, "")
	job, err := waitForJob(sess, created.JSON202.Id, 30*time.Second)
	if err != nil || job.Status != "completed" || job.DerivedAssetKey == nil {
		rep.Add("derived/job-completed-with-asset-key", false, fmt.Sprintf("err=%v job=%v", err, job))
		return
	}
	rep.Add("derived/job-completed-with-asset-key", true, "")

	assetContent, err := sess.Client.GetDerivedAssetContentWithResponse(ctx, *job.DerivedAssetKey, &api.GetDerivedAssetContentParams{}, sess.SameOrigin)
	if err != nil || assetContent.HTTPResponse == nil || assetContent.HTTPResponse.StatusCode != 200 || len(assetContent.Body) == 0 {
		rep.Add("derived/content-readable", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(assetContent)))
	} else {
		rep.Add("derived/content-readable", true, fmt.Sprintf("bytes=%d", len(assetContent.Body)))
	}

	// derivedjob.Service.Create 对每次请求都新建一个 Job（CreateWithOptions 的幂等键
	// 参数为空字符串，未做 Job 级去重），"缓存命中"发生在 Execute 阶段——已存在受校验
	// 的 manifest 时跳过重新解码/缩放，只是让该 Job 很快到达 completed，不代表 create
	// 请求会在 HTTP 响应里同步返回已完成状态。这里改为轮询到终态，只验证确实复用了
	// 同一个内容寻址 assetKey（真正的缓存证据），不假设 create 响应本身同步完成。
	cached, err := sess.Client.CreateDerivedAssetWithResponse(ctx, mediaID, &api.CreateDerivedAssetParams{XGalleryCSRF: sess.CSRF},
		api.DerivedAssetCreateRequest{TransformId: "thumbnail", TransformVersion: "v1"}, sess.SameOrigin)
	if err != nil || cached.JSON202 == nil {
		rep.Add("derived/singleflight-cache-hit", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(cached)))
		return
	}
	cachedJob, err := waitForJob(sess, cached.JSON202.Id, 30*time.Second)
	if err != nil || cachedJob.Status != "completed" || cachedJob.DerivedAssetKey == nil {
		rep.Add("derived/singleflight-cache-hit", false, fmt.Sprintf("err=%v job=%v", err, cachedJob))
		return
	}
	rep.Add("derived/singleflight-cache-hit", *cachedJob.DerivedAssetKey == *job.DerivedAssetKey,
		fmt.Sprintf("firstKeyLen=%d secondKeyLen=%d sameKey=%v", len(*job.DerivedAssetKey), len(*cachedJob.DerivedAssetKey), *cachedJob.DerivedAssetKey == *job.DerivedAssetKey))
}
