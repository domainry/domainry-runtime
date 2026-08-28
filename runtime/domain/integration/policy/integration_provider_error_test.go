package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestNormalizeProviderErrorUsesStableCategoriesAndPreservesCancellation(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantRaw    string
		wantCancel bool
	}{
		{name: "legacy unsupported", err: errors.New("backend.integration.email.operation_unsupported"), wantCode: "backend.integration.operation_unsupported", wantRaw: "backend.integration.email.operation_unsupported"},
		{name: "typed rejected", err: NewProviderError(ErrorProviderRejected, "stripe.card_declined", errors.New("declined")), wantCode: "backend.integration.provider_rejected", wantRaw: "stripe.card_declined"},
		{name: "typed retry", err: NewProviderError(ErrorRetryExhausted, "provider.retry_limit", errors.New("retry limit")), wantCode: "backend.integration.retry_exhausted", wantRaw: "provider.retry_limit"},
		{name: "public retryable", err: NewProviderError(ErrorProviderRetryable, "provider.temporary", errors.New("temporary")), wantCode: "backend.integration.provider_retryable", wantRaw: "provider.temporary"},
		{name: "public permanent", err: NewProviderError(ErrorProviderPermanent, "provider.invalid", errors.New("invalid")), wantCode: "backend.integration.provider_permanent", wantRaw: "provider.invalid"},
		{name: "public uncertain", err: NewProviderError(ErrorProviderUncertain, "provider.unknown", errors.New("unknown")), wantCode: "backend.integration.provider_outcome_uncertain", wantRaw: "provider.unknown"},
		{name: "cancelled", err: context.Canceled, wantCancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, raw := NormalizeProviderError(test.err)
			if test.wantCancel {
				if !errors.Is(actual, context.Canceled) || raw != "" {
					t.Fatalf("actual=%v raw=%q", actual, raw)
				}
				return
			}
			var appError *apperror.AppError
			if !errors.As(actual, &appError) || appError.Code != test.wantCode || raw != test.wantRaw {
				t.Fatalf("code=%q raw=%q error=%v", appError.Code, raw, actual)
			}
		})
	}
}

func TestProviderErrorContractClassificationAndNormalizationEdges(t *testing.T) {
	base := errors.New("provider detail")
	withCode := &ProviderError{Category: ErrorProviderRejected, Code: " code ", Err: base}
	if withCode.Error() != "code" || !errors.Is(withCode, base) {
		t.Fatalf("coded provider error = %q/%v", withCode.Error(), withCode.Unwrap())
	}
	withoutCode := &ProviderError{Category: ErrorProviderRejected, Err: base}
	if withoutCode.Error() != "provider detail" || ProviderErrorCode(withoutCode) != "provider detail" {
		t.Fatalf("wrapped provider error = %q", withoutCode.Error())
	}
	categoryOnly := &ProviderError{Category: ErrorRetryExhausted}
	if categoryOnly.Error() != string(ErrorRetryExhausted) {
		t.Fatalf("category provider error = %q", categoryOnly.Error())
	}
	if category, ok := ErrorCategoryOf(nil); ok || category != "" {
		t.Fatalf("nil category = %q/%v", category, ok)
	}
	if category, ok := ErrorCategoryOf(errors.New("provider.operation_not_supported")); !ok || category != ErrorOperationUnsupported {
		t.Fatalf("legacy alternate category = %q/%v", category, ok)
	}
	if category, ok := ErrorCategoryOf(errors.New("ordinary")); ok || category != "" {
		t.Fatalf("ordinary category = %q/%v", category, ok)
	}
	if category, ok := ErrorCategoryOf(&ProviderError{Err: errors.New("ordinary")}); ok || category != "" {
		t.Fatalf("empty typed category = %q/%v", category, ok)
	}
	if ProviderErrorCode(nil) != "" || ProviderErrorCode(withCode) != "code" {
		t.Fatal("provider error code edge mismatch")
	}
	if normalized, raw := NormalizeProviderError(nil); normalized != nil || raw != "" {
		t.Fatalf("nil normalization = %v/%q", normalized, raw)
	}
	if normalized, raw := NormalizeProviderError(context.DeadlineExceeded); !errors.Is(normalized, context.DeadlineExceeded) || raw != "" {
		t.Fatalf("deadline normalization = %v/%q", normalized, raw)
	}
	ordinary := errors.New("ordinary")
	if normalized, raw := NormalizeProviderError(ordinary); normalized != ordinary || raw != "" {
		t.Fatalf("ordinary normalization = %v/%q", normalized, raw)
	}
	unknown := &ProviderError{Category: ErrorCategory("future"), Code: "future.code"}
	if normalized, raw := NormalizeProviderError(unknown); normalized != unknown || raw != "future.code" {
		t.Fatalf("unknown normalization = %v/%q", normalized, raw)
	}
}

func TestProviderFailureDisposition(t *testing.T) {
	tests := []struct {
		err  error
		want FailureDisposition
	}{
		{err: context.Canceled, want: FailureDispositionCancelled},
		{err: context.DeadlineExceeded, want: FailureDispositionCancelled},
		{err: NewProviderError(ErrorOperationUnsupported, "provider.unsupported", nil), want: FailureDispositionTerminal},
		{err: NewProviderError(ErrorRetryExhausted, "provider.retry", nil), want: FailureDispositionTerminal},
		{err: NewProviderError(ErrorProviderRejected, "provider.rejected", nil), want: FailureDispositionTerminal},
		{err: NewProviderError(ErrorProviderRetryable, "provider.temporary", nil), want: FailureDispositionTransient},
		{err: NewProviderError(ErrorProviderPermanent, "provider.invalid", nil), want: FailureDispositionTerminal},
		{err: NewProviderError(ErrorProviderUncertain, "provider.unknown", nil), want: FailureDispositionTerminal},
		{err: errors.New("provider rate_limited"), want: FailureDispositionRateLimited},
		{err: errors.New("provider too_many_requests"), want: FailureDispositionRateLimited},
		{err: errors.New("provider http_status_429"), want: FailureDispositionRateLimited},
		{err: errors.New("provider http_429"), want: FailureDispositionRateLimited},
		{err: errors.New("provider connection timeout"), want: FailureDispositionDependencyUnavailable},
		{err: errors.New("provider unavailable"), want: FailureDispositionDependencyUnavailable},
		{err: errors.New("provider connection lost"), want: FailureDispositionDependencyUnavailable},
		{err: errors.New("temporary provider error"), want: FailureDispositionTransient},
	}
	for _, test := range tests {
		if got := ProviderFailureDisposition(test.err); got != test.want {
			t.Fatalf("classify %v: got=%s want=%s", test.err, got, test.want)
		}
	}
}
