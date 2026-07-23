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
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Scheme == "" || !strings.EqualFold(origin.Host, r.Host) || (origin.Scheme != "http" && origin.Scheme != "https") {
		return fault.New(fault.CodeOriginRejected, false, nil)
	}
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
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
