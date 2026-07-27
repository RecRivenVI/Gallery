package query

import (
	"fmt"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

// RunCursorCorrectness 覆盖第八节「cursor」必测维度中可以在单个 galleryd 实例内验证
// 的部分：query/sort 绑定、rankProtocolVersion、queryPublicationId 回显、HMAC 篡改、
// 错误 query/sort 复用一律 CURSOR_EXPIRED，篡改一律 CURSOR_INVALID。跨快照续页
// （构建期间持续翻页）需要驱动第二次发布，不在本函数覆盖范围内。
func RunCursorCorrectness(rep *report.Report, sess *environment.Session) {
	first, err := listWorks(sess, api.ListWorksParams{Limit: ptr(50)})
	if err != nil || first.JSON200 == nil || first.JSON200.NextCursor == nil {
		rep.Add("cursor/first-page-issues-cursor", false, fmt.Sprintf("err=%v status=%d nextCursor-present=%v", err, environment.StatusOf(first), first != nil && first.JSON200 != nil && first.JSON200.NextCursor != nil))
		return
	}
	rep.Add("cursor/first-page-issues-cursor", true, "")
	cursor := *first.JSON200.NextCursor
	publicationID := first.JSON200.QueryPublicationId

	second, err := listWorks(sess, api.ListWorksParams{Limit: ptr(50), Cursor: ptr(cursor)})
	if err != nil || second.JSON200 == nil {
		rep.Add("cursor/continuation-succeeds", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(second)))
	} else {
		rep.Add("cursor/continuation-succeeds", second.JSON200.QueryPublicationId == publicationID, fmt.Sprintf("pub=%s expected=%s", second.JSON200.QueryPublicationId, publicationID))
	}

	tampered := tamperCursor(cursor)
	tamperedResp, err := listWorks(sess, api.ListWorksParams{Limit: ptr(50), Cursor: ptr(tampered)})
	if err != nil || tamperedResp.JSON400 == nil || tamperedResp.JSON400.Error.Code != api.CURSORINVALID {
		rep.Add("cursor/hmac-tamper-rejected", false, fmt.Sprintf("err=%v status=%d code=%v", err, environment.StatusOf(tamperedResp), errorCodeOf(tamperedResp)))
	} else {
		rep.Add("cursor/hmac-tamper-rejected", true, "")
	}

	// 换用不同的搜索词复用同一个游标：查询指纹变化必须使旧游标续页失败为 CURSOR_EXPIRED。
	wrongQuery, err := listWorks(sess, api.ListWorksParams{Q: ptr("普通作品"), Limit: ptr(50), Cursor: ptr(cursor)})
	if err != nil || wrongQuery.JSON409 == nil {
		rep.Add("cursor/reuse-with-different-query-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(wrongQuery)))
	} else {
		rep.Add("cursor/reuse-with-different-query-rejected", wrongQuery.JSON409.Error.Code == api.CURSOREXPIRED, fmt.Sprintf("code=%v", wrongQuery.JSON409.Error.Code))
	}

	// 换用不同排序复用同一个游标：同样必须 CURSOR_EXPIRED。
	wrongSort, err := listWorks(sess, api.ListWorksParams{Limit: ptr(50), Cursor: ptr(cursor), Sort: ptr(api.ListWorksParamsSort("title_desc"))})
	if err != nil || wrongSort.JSON409 == nil {
		rep.Add("cursor/reuse-with-different-sort-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(wrongSort)))
	} else {
		rep.Add("cursor/reuse-with-different-sort-rejected", wrongSort.JSON409.Error.Code == api.CURSOREXPIRED, fmt.Sprintf("code=%v", wrongSort.JSON409.Error.Code))
	}

	// 裸 revision 组合拒绝：queryPublicationId 必须是服务端签发的合法 ID，任意伪造
	// 字符串不得被当作合法快照静默接受、也不得静默回退到 active。internal/query
	// 格式合法但从未由服务端签发的 publication 必须按已回收快照处理，不得回退 current。
	forged, err := listWorks(sess, api.ListWorksParams{Limit: ptr(20), QueryPublicationId: ptr(api.QueryPublicationId("qpub_018f47d2-5c16-7a44-a8a0-0000000000ff"))})
	switch {
	case err != nil:
		rep.Add("cursor/forged-publication-id-rejected", false, fmt.Sprintf("err=%v status=%d", err, environment.StatusOf(forged)))
	case forged.JSON409 != nil && forged.JSON409.Error.Code == api.CURSOREXPIRED:
		rep.Add("cursor/forged-publication-id-rejected", true, "")
	default:
		rep.Add("cursor/forged-publication-id-rejected", false, fmt.Sprintf("status=%d", environment.StatusOf(forged)))
	}
}

func tamperCursor(cursor string) string {
	runes := []rune(cursor)
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] >= 'a' && runes[i] <= 'z' {
			if runes[i] == 'a' {
				runes[i] = 'b'
			} else {
				runes[i] = 'a'
			}
			return string(runes)
		}
	}
	return cursor + "x"
}

func errorCodeOf(r any) string {
	switch v := r.(type) {
	case *api.ListWorksResponse:
		if v.JSON400 != nil {
			return string(v.JSON400.Error.Code)
		}
	}
	return ""
}
