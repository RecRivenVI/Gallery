package auth

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

const CSRFHeader = "X-Gallery-CSRF"

func ValidateLoopbackRequest(r *http.Request) error {
	if err := ValidateHost(r); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isLoopback(host) {
		return fault.New(fault.CodeForbidden, false, nil)
	}
	return nil
}

func ValidateHost(r *http.Request) error {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" || !isLoopback(host) {
		return fault.New(fault.CodeHostRejected, false, nil)
	}
	return nil
}

func ValidateHostAllowed(r *http.Request, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		return ValidateHost(r)
	}
	for _, allowed := range allowedHosts {
		if strings.EqualFold(r.Host, allowed) {
			return nil
		}
	}
	return fault.New(fault.CodeHostRejected, false, nil)
}

func ValidateMutation(r *http.Request, expectedCSRF string) error {
	if err := ValidateOrigin(r); err != nil {
		return err
	}
	provided := r.Header.Get(CSRFHeader)
	if expectedCSRF == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expectedCSRF)) != 1 {
		return fault.New(fault.CodeCSRFInvalid, false, nil)
	}
	return nil
}

func ValidateOrigin(r *http.Request) error {
	if err := ValidateHost(r); err != nil {
		return err
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Scheme == "" || !strings.EqualFold(origin.Host, r.Host) || (origin.Scheme != "http" && origin.Scheme != "https") {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	return nil
}

func ValidateOriginAllowed(r *http.Request, allowedHosts []string) error {
	if err := ValidateHostAllowed(r, allowedHosts); err != nil {
		return err
	}
	if err := validateSameOriginHeader(r); err != nil {
		return err
	}
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	return nil
}

// ValidateWebSocketOriginAllowed 是 WebSocket 握手专用的同源校验。
//
// 浏览器**不会**在 WebSocket 握手请求上发送 Fetch Metadata 头：Chrome 与 Edge 的同源
// `ws://` 握手只带 `Origin`，完全不带任何 `Sec-Fetch-*`。因此复用要求
// `Sec-Fetch-Site: same-origin` 的 ValidateOriginAllowed 会让 `/ws/v1` 对所有主流浏览器
// 恒定返回 ORIGIN_REJECTED，实时通道永远无法建立。
//
// 跨站 WebSocket 劫持的防护由 Origin 本身承担：浏览器在 WebSocket 握手上始终发送
// 真实 Origin 且不可被页面脚本伪造，攻击页面的 Origin 与服务端 Host 不同即被拒绝。
// Fetch Metadata 在此只作纵深防御——存在时仍必须是 same-origin，缺失时不构成拒绝理由。
func ValidateWebSocketOriginAllowed(r *http.Request, allowedHosts []string) error {
	if err := ValidateHostAllowed(r, allowedHosts); err != nil {
		return err
	}
	if err := validateSameOriginHeader(r); err != nil {
		return err
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	return nil
}

func validateSameOriginHeader(r *http.Request) error {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Scheme == "" || !strings.EqualFold(origin.Host, r.Host) || (origin.Scheme != "http" && origin.Scheme != "https") {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	return nil
}

func ValidateMutationAllowed(r *http.Request, expectedCSRF string, allowedHosts []string) error {
	if err := ValidateOriginAllowed(r, allowedHosts); err != nil {
		return err
	}
	provided := r.Header.Get(CSRFHeader)
	if expectedCSRF == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expectedCSRF)) != 1 {
		return fault.New(fault.CodeCSRFInvalid, false, nil)
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(strings.Trim(host, "[]"), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
