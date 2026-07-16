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

func ValidateMutation(r *http.Request, expectedCSRF string) error {
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
