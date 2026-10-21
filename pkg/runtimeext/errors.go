package runtimeext

import "strings"

const (
	BusinessErrorBadRequest  = "bad_request"
	BusinessErrorForbidden   = "forbidden"
	BusinessErrorNotFound    = "not_found"
	BusinessErrorConflict    = "conflict"
	BusinessErrorRateLimited = "rate_limited"
	BusinessErrorUnavailable = "unavailable"
	BusinessErrorInternal    = "internal"
)

// BusinessError is a stable project-owned error safe for Runtime mapping and
// localization. Class is the closed application outcome consumed by Runtime;
// an empty Class preserves the bad-request default. Message is a developer
// fallback; Code and Parameters are the public contract.
type BusinessError struct {
	Class      string
	Code       string
	Message    string
	Parameters map[string]string
	Cause      error
}

func (e *BusinessError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func (e *BusinessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *BusinessError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Code)
}

func (e *BusinessError) ErrorParams() map[string]string {
	if e == nil || len(e.Parameters) == 0 {
		return nil
	}
	result := make(map[string]string, len(e.Parameters))
	for key, value := range e.Parameters {
		result[key] = value
	}
	return result
}

func (e *BusinessError) Valid() bool {
	if e == nil || strings.TrimSpace(e.Code) == "" {
		return false
	}
	switch strings.TrimSpace(e.Class) {
	case "", BusinessErrorBadRequest, BusinessErrorForbidden, BusinessErrorNotFound, BusinessErrorConflict, BusinessErrorRateLimited, BusinessErrorUnavailable, BusinessErrorInternal:
		return true
	default:
		return false
	}
}
