package policy

import (
	"context"
	"errors"
	"io"
	"strings"
)

// IntegrationProviderOutcomeUncertain identifies failures where a side effect
// may have reached the provider but no authoritative local receipt exists.
// These outcomes require reconciliation and must never enter automatic retry.
func IntegrationProviderOutcomeUncertain(err error, responseRef string) bool {
	if ProviderResponseProvesNoEffect(err, responseRef) {
		return false
	}
	if strings.TrimSpace(responseRef) != "" {
		return true
	}
	if err == nil {
		return false
	}
	if category, ok := ErrorCategoryOf(err); ok && category == ErrorProviderUncertain {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"timeout", "timed out", "connection reset", "broken pipe", "partial response", "uncertain success", "outcome unknown"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// ProviderResponseProvesNoEffect identifies explicit provider rejections that
// prove the write was not accepted. A 429 response is safe to retry even for a
// non-idempotent operation; timeouts, disconnects and 5xx responses remain
// uncertain because the provider may have completed the side effect.
func ProviderResponseProvesNoEffect(err error, responseRef string) bool {
	ref := strings.ToLower(strings.TrimSpace(responseRef))
	text := ref
	if err != nil {
		text += " " + strings.ToLower(ProviderErrorCode(err)) + " " + strings.ToLower(err.Error())
	}
	return ref == "http:429" || strings.Contains(text, "http_status_429") || strings.Contains(text, "http_429") || strings.Contains(text, "too_many_requests")
}
