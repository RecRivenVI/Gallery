package fault_test

import (
	"fmt"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

func TestEveryStableCodeIsAcceptedByErrorSchema(t *testing.T) {
	validator, err := fault.NewErrorValidator()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range fault.AllCodes() {
		data := []byte(fmt.Sprintf(`{"error":{"code":%q,"retryable":false,"correlationId":"test"}}`, code))
		if err := validator.ValidateJSON(data); err != nil {
			t.Fatalf("code %s 未进入错误 Schema: %v", code, err)
		}
	}
}
