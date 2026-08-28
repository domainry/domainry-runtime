package policy

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestIntegrationProviderOutcomeUncertainSeparatesReconciliationFromRetry(t *testing.T) {
	for _, test := range []struct {
		name        string
		err         error
		responseRef string
		want        bool
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "EOF", err: io.EOF, want: true},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "nil", err: nil, want: false},
		{name: "connection reset", err: errors.New("connection reset by peer"), want: true},
		{name: "partial response", err: errors.New("partial response"), want: true},
		{name: "receipt with error", err: errors.New("provider error"), responseRef: "provider:42", want: true},
		{name: "rate limit", err: errors.New("http_429 rate_limit"), want: false},
		{name: "rate limit response", err: errors.New("backend.integration.google.http_status_429"), responseRef: "http:429", want: false},
		{name: "server error response", err: errors.New("backend.integration.google.http_status_503"), responseRef: "http:503", want: true},
		{name: "clear rejection", err: errors.New("provider rejected request"), want: false},
		{name: "typed uncertain", err: NewProviderError(ErrorProviderUncertain, "provider.outcome_unknown", errors.New("redacted")), want: true},
		{name: "typed retryable", err: NewProviderError(ErrorProviderRetryable, "provider.temporary", errors.New("redacted")), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IntegrationProviderOutcomeUncertain(test.err, test.responseRef); got != test.want {
				t.Fatalf("uncertain=%v want=%v", got, test.want)
			}
		})
	}
	if !ProviderResponseProvesNoEffect(errors.New("backend.integration.google.http_status_429"), "http:429") || ProviderResponseProvesNoEffect(errors.New("backend.integration.google.http_status_503"), "http:503") {
		t.Fatal("provider clear-rejection evidence was classified incorrectly")
	}
}
