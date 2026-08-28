package policy

import (
	"context"
	"errors"
	"strings"

	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type ErrorCategory string

const (
	ErrorOperationUnsupported ErrorCategory = "operation_unsupported"
	ErrorProviderRejected     ErrorCategory = "provider_rejected"
	ErrorRetryExhausted       ErrorCategory = "retry_exhausted"
	ErrorProviderRetryable    ErrorCategory = "provider_retryable"
	ErrorProviderPermanent    ErrorCategory = "provider_permanent"
	ErrorProviderUncertain    ErrorCategory = "provider_uncertain"
)

type ProviderError struct {
	Category ErrorCategory
	Code     string
	Err      error
}

func (e *ProviderError) Error() string {
	if strings.TrimSpace(e.Code) != "" {
		return strings.TrimSpace(e.Code)
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Category)
}

func (e *ProviderError) Unwrap() error { return e.Err }

func NewProviderError(category ErrorCategory, code string, err error) error {
	return &ProviderError{Category: category, Code: strings.TrimSpace(code), Err: err}
}

func ErrorCategoryOf(err error) (ErrorCategory, bool) {
	if err == nil {
		return "", false
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) && providerError.Category != "" {
		return providerError.Category, true
	}
	code := strings.TrimSpace(err.Error())
	if strings.HasSuffix(code, ".operation_unsupported") || strings.HasSuffix(code, ".operation_not_supported") {
		return ErrorOperationUnsupported, true
	}
	return "", false
}

func ProviderErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) && strings.TrimSpace(providerError.Code) != "" {
		return strings.TrimSpace(providerError.Code)
	}
	return strings.TrimSpace(err.Error())
}

func NormalizeProviderError(err error) (error, string) {
	if err == nil {
		return nil, ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err, ""
	}
	providerCode := ProviderErrorCode(err)
	category, classified := ErrorCategoryOf(err)
	if !classified {
		return err, ""
	}
	params := map[string]string{"provider_error_code": providerCode}
	switch category {
	case ErrorOperationUnsupported:
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.operation_unsupported", Params: params, Err: err}, providerCode
	case ErrorProviderRejected:
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.provider_rejected", Params: params, Err: err}, providerCode
	case ErrorRetryExhausted:
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.integration.retry_exhausted", Params: params, Err: err}, providerCode
	case ErrorProviderRetryable:
		return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.integration.provider_retryable", Params: params, Err: err}, providerCode
	case ErrorProviderPermanent:
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.provider_permanent", Params: params, Err: err}, providerCode
	case ErrorProviderUncertain:
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.integration.provider_outcome_uncertain", Params: params, Err: err}, providerCode
	default:
		return err, providerCode
	}
}

// FailureDisposition preserves provider-specific business semantics without
// coupling the Domain layer to the process-level worker package.
type FailureDisposition string

const (
	FailureDispositionTransient             FailureDisposition = "transient"
	FailureDispositionRateLimited           FailureDisposition = "rate_limited"
	FailureDispositionDependencyUnavailable FailureDisposition = "dependency_unavailable"
	FailureDispositionTerminal              FailureDisposition = "terminal"
	FailureDispositionCancelled             FailureDisposition = "cancelled"
)

func ProviderFailureDisposition(err error) FailureDisposition {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureDispositionCancelled
	}
	if category, ok := ErrorCategoryOf(err); ok {
		switch category {
		case ErrorProviderRetryable:
			return FailureDispositionTransient
		case ErrorOperationUnsupported, ErrorProviderRejected, ErrorRetryExhausted, ErrorProviderPermanent, ErrorProviderUncertain:
			return FailureDispositionTerminal
		}
	}
	text := strings.ToLower(ProviderErrorCode(err))
	switch {
	case strings.Contains(text, "rate_limit"), strings.Contains(text, "too_many_requests"), strings.Contains(text, "http_status_429"), strings.Contains(text, "http_429"):
		return FailureDispositionRateLimited
	case strings.Contains(text, "timeout"), strings.Contains(text, "unavailable"), strings.Contains(text, "connection"):
		return FailureDispositionDependencyUnavailable
	}
	return FailureDispositionTransient
}
