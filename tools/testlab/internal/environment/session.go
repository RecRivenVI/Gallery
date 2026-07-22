// Package environment 建立并持有一次针对真实 galleryd 实例的 Personal 管理
// Session：只使用 pkg/galleryapi 生成的公开契约客户端与真实 HTTP loopback 连接，
// 不导入任何 internal 包，也不直接读取数据库。stage3/stage4/未来阶段的
// orchestrator 共用同一个 Session 建立与调用入口，不各自重新实现配对握手。
package environment

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

// Session 是一次通过公开一次性配对流程建立的 Personal 管理 Session。
type Session struct {
	Client     *api.ClientWithResponses
	CSRF       api.CSRFHeader
	SameOrigin api.RequestEditorFn
	BaseURL    string
}

// NewBareSession 只建立生成客户端与同源请求编辑器，不执行配对握手；供测试用假
// HTTP 服务器（没有实现 bootstrap/pairing 端点）直接构造可用于调用具体业务接口
// （如 ListWorks）的 Session。生产路径请使用 NewSession。
func NewBareSession(baseURL string) (*Session, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client, err := api.NewClientWithResponses(baseURL, api.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		return nil, err
	}
	editor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Origin", baseURL)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		return nil
	}
	return &Session{Client: client, SameOrigin: editor, BaseURL: baseURL}, nil
}

// NewSession 建立一个真正完成一次性配对握手的 Session。
func NewSession(baseURL string) (*Session, error) {
	bare, err := NewBareSession(baseURL)
	if err != nil {
		return nil, err
	}
	client, editor := bare.Client, bare.SameOrigin

	ctx := context.Background()
	bootstrap, err := client.GetBootstrapWithResponse(ctx)
	if err != nil || bootstrap.JSON200 == nil {
		return nil, fmt.Errorf("bootstrap 失败: %v status=%d", err, StatusOf(bootstrap))
	}
	attempt, err := client.CreatePairingAttemptWithResponse(ctx, &api.CreatePairingAttemptParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken}, editor)
	if err != nil || attempt.JSON201 == nil {
		return nil, fmt.Errorf("创建配对 attempt 失败: %v status=%d", err, StatusOf(attempt))
	}
	exchange, err := client.ExchangePairingCredentialWithResponse(ctx, &api.ExchangePairingCredentialParams{XGalleryCSRF: bootstrap.JSON200.CsrfToken},
		api.PairingExchangeRequest{Credential: attempt.JSON201.Credential}, editor)
	if err != nil || exchange.JSON201 == nil {
		return nil, fmt.Errorf("配对交换失败: %v status=%d", err, StatusOf(exchange))
	}
	return &Session{Client: client, CSRF: exchange.JSON201.CsrfToken, SameOrigin: editor, BaseURL: baseURL}, nil
}

// ListWorks 是 stage3/stage4 各阶段最常用的只读查询入口，统一在此实现一次，避免
// 每个阶段包各自复制同一行客户端调用。
func (s *Session) ListWorks(params api.ListWorksParams) (*api.ListWorksResponse, error) {
	return s.Client.ListWorksWithResponse(context.Background(), &params, s.SameOrigin)
}

type statusResponse interface{ StatusCode() int }

// StatusOf 从任意生成客户端响应类型中提取 HTTP 状态码，取不到时返回 0。
func StatusOf(r any) int {
	if s, ok := r.(statusResponse); ok {
		return s.StatusCode()
	}
	return 0
}
