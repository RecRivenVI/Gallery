package main

import (
	"testing"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
)

type fakeResponse struct {
	code int
}

func (response *fakeResponse) StatusCode() int {
	return response.code
}

func TestStatusCodeHandlesTypedNil(t *testing.T) {
	var response *fakeResponse
	if got := statusCode(response); got != 0 {
		t.Fatalf("typed nil status=%d", got)
	}
	if got := statusCode(&fakeResponse{code: 202}); got != 202 {
		t.Fatalf("status=%d", got)
	}
}

func TestValidateLibraryPresence(t *testing.T) {
	libraries := []api.Library{{Name: beforeBackupLibrary}, {Name: afterBackupLibrary}}
	if err := validateLibraryPresence(libraries, map[string]bool{
		beforeBackupLibrary: true,
		afterBackupLibrary:  true,
		"missing":           false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateLibraryPresence(libraries, map[string]bool{"missing": true}); err == nil {
		t.Fatal("缺失 Library 未被拒绝")
	}
}
