package realtime

import (
	_ "embed"

	contractschema "github.com/RecRivenVI/gallery/internal/contract/schema"
)

//go:embed envelope.schema.json
var envelopeSchema []byte

func EnvelopeSchema() []byte { return append([]byte(nil), envelopeSchema...) }

func NewEnvelopeValidator() (*contractschema.Validator, error) {
	return contractschema.Compile("envelope.schema.json", envelopeSchema)
}
