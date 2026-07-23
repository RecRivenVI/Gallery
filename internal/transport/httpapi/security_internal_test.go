package httpapi

import (
	"net/http"
	"testing"
)

func TestLoginRateSubjectIgnoresEphemeralPortAndProxyHeaders(t *testing.T) {
	first := &http.Request{RemoteAddr: "192.168.1.25:41000", Header: http.Header{
		"X-Forwarded-For": []string{"203.0.113.9"},
	}}
	second := &http.Request{RemoteAddr: "192.168.1.25:51000", Header: http.Header{
		"X-Forwarded-For": []string{"198.51.100.4"},
	}}
	if got, want := loginRateSubject(first), "192.168.1.25"; got != want {
		t.Fatalf("first subject=%q want=%q", got, want)
	}
	if got, want := loginRateSubject(second), "192.168.1.25"; got != want {
		t.Fatalf("second subject=%q want=%q", got, want)
	}
	if loginRateSubject(first) != loginRateSubject(second) {
		t.Fatal("同一对端 IP 的不同临时端口必须共享登录限流主体")
	}
}

