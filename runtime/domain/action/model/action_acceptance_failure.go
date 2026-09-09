package actionmodel

import (
	"context"
	"strings"
)

// AcceptanceFailureHeader names the request header that injects a controlled
// failure into one governed Action invocation. Transports honor it only when
// development identity headers are enabled; production listeners ignore it.
const AcceptanceFailureHeader = "X-Domainry-Acceptance-Failure"

// AcceptanceFailureBeforeCommit fails the invocation after the Business
// Handler has staged every mutation and before the Action transaction commits,
// proving that parent and child writes roll back together.
const AcceptanceFailureBeforeCommit = "before_commit"

// AcceptanceFailureInjectedCode is the internal error reported for an injected
// acceptance failure.
const AcceptanceFailureInjectedCode = "backend.action.acceptance_failure_injected"

type acceptanceFailureContextKey struct{}

// WithAcceptanceFailurePoint attaches one acceptance failure point to ctx.
func WithAcceptanceFailurePoint(ctx context.Context, point string) context.Context {
	point = strings.TrimSpace(point)
	if point == "" {
		return ctx
	}
	return context.WithValue(ctx, acceptanceFailureContextKey{}, point)
}

// AcceptanceFailurePoint returns the injected failure point, if any.
func AcceptanceFailurePoint(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	point, _ := ctx.Value(acceptanceFailureContextKey{}).(string)
	return point
}
