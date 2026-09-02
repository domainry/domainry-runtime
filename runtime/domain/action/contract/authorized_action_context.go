package contract

import (
	"context"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

type authorizedActionContextKey struct{}

// WithAuthorizedAction records the normalized Action that the request gate
// already authorized. Application use cases may use this provenance to
// constrain an Action-owned mutation without checking a second Permission.
func WithAuthorizedAction(ctx context.Context, definition actioncontract.ActionDefinition) context.Context {
	if ctx == nil || strings.TrimSpace(definition.Key) == "" {
		return ctx
	}
	return context.WithValue(ctx, authorizedActionContextKey{}, definition)
}

// AuthorizedActionFromContext returns only Action provenance written by the
// Runtime authorization gate or another trusted application entrypoint.
func AuthorizedActionFromContext(ctx context.Context) (actioncontract.ActionDefinition, bool) {
	if ctx == nil {
		return actioncontract.ActionDefinition{}, false
	}
	definition, ok := ctx.Value(authorizedActionContextKey{}).(actioncontract.ActionDefinition)
	return definition, ok && strings.TrimSpace(definition.Key) != ""
}
