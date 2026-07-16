package query

import (
	_ "embed"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

//go:embed cursor.schema.json
var cursorSchema []byte

func CursorSchema() []byte { return append([]byte(nil), cursorSchema...) }

func NewCursorValidator() (*contractschema.Validator, error) {
	return contractschema.Compile("cursor.schema.json", cursorSchema)
}
