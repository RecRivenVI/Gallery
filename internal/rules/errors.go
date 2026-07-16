package rules

import "errors"

type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func withField(field string, err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: field, Err: err}
}

func ErrorField(err error) string {
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		return fieldError.Field
	}
	return ""
}
