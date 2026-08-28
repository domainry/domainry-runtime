package runtimeext

import "strings"

// BusinessError is a stable project-owned error safe for Runtime mapping and
// localization. Message is a developer fallback; Code and Parameters are the
// public contract.
type BusinessError struct {
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
	return e != nil && strings.TrimSpace(e.Code) != ""
}
