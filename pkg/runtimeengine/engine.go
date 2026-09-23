// Package runtimeengine is the public in-process application boundary for
// project-owned HTTP handlers. It exposes governed Runtime use cases, never
// repositories, SQL handles, or transport-owned generic routes.
package runtimeengine

import (
	"context"
	"errors"
	"net/http"
)

type Record struct {
	ID        string         `json:"id"`
	ObjectKey string         `json:"object_key"`
	Fields    map[string]any `json:"fields"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type Sort struct {
	Field     string
	Direction string
}

type Query struct {
	Page         int
	PageSize     int
	Search       string
	SearchFields []string
	Filters      map[string]any
	Sorts        []Sort
	SelectFields []string
	AfterID      string
}

type Page struct {
	Items       []Record
	Page        int
	PageSize    int
	Total       int
	HasNext     bool
	NextAfterID string
}

type ActionRequest struct {
	ActionKey            string
	ObjectKey            string
	RecordID             string
	Input                any
	IdempotencyKey       string
	TargetOrganizationID string
	AssuranceToken       string
}

type ActionResult struct {
	InvocationID string
	Status       string
	Output       map[string]any
	Record       *Record
}

// Engine is safe for project-owned HTTP handlers. Ordinary endpoints call the
// record methods. A real business operation calls InvokeAction, which retains
// Runtime's existing governed BusinessHandler transaction and capability model.
type Engine interface {
	List(context.Context, string, Query) (Page, error)
	Get(context.Context, string, string) (Record, error)
	Create(context.Context, string, any) (Record, error)
	Update(context.Context, string, string, any) (Record, error)
	Delete(context.Context, string, string) error
	InvokeAction(context.Context, ActionRequest) (ActionResult, error)
}

// HTTPFactory receives the authenticated Runtime Engine and returns the
// project's own router. Runtime mounts it below /api/ and applies its normal
// authentication, workspace admission, capacity, recovery and CORS layers.
type HTTPFactory func(Engine) http.Handler

type ErrorKind string

const (
	ErrorBadRequest      ErrorKind = "bad_request"
	ErrorUnauthenticated ErrorKind = "unauthenticated"
	ErrorForbidden       ErrorKind = "forbidden"
	ErrorNotFound        ErrorKind = "not_found"
	ErrorConflict        ErrorKind = "conflict"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorInternal        ErrorKind = "internal"
)

type Error struct {
	Kind       ErrorKind
	Code       string
	Message    string
	Parameters map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Cause }

func HTTPStatus(err error) int {
	var typed *Error
	if !errors.As(err, &typed) {
		return http.StatusInternalServerError
	}
	switch typed.Kind {
	case ErrorBadRequest:
		return http.StatusBadRequest
	case ErrorUnauthenticated:
		return http.StatusUnauthorized
	case ErrorForbidden:
		return http.StatusForbidden
	case ErrorNotFound:
		return http.StatusNotFound
	case ErrorConflict:
		return http.StatusConflict
	case ErrorRateLimited:
		return http.StatusTooManyRequests
	case ErrorUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func ErrorCode(err error) string {
	var typed *Error
	if errors.As(err, &typed) && typed.Code != "" {
		return typed.Code
	}
	return "backend.internal"
}

func AsError(err error) (*Error, bool) {
	var typed *Error
	ok := errors.As(err, &typed)
	return typed, ok
}

func NewError(kind ErrorKind, code string, parameters map[string]string, cause error) error {
	return &Error{Kind: kind, Code: code, Parameters: cloneStrings(parameters), Cause: cause}
}

func cloneStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
