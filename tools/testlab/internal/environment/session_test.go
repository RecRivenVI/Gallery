package environment

import "testing"

type testStatusResponse struct{ code int }

func (r *testStatusResponse) StatusCode() int { return r.code }

func TestStatusOfTreatsTypedNilResponseAsNoStatus(t *testing.T) {
	var response *testStatusResponse
	if got := StatusOf(response); got != 0 {
		t.Fatalf("typed nil status=%d, want 0", got)
	}
	if got := StatusOf(&testStatusResponse{code: 202}); got != 202 {
		t.Fatalf("response status=%d, want 202", got)
	}
}
