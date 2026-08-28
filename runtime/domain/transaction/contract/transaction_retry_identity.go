package contract

import (
	"context"
	"strings"
)

type transactionRetryIdentityContextKey struct{}

type TransactionIdempotencyScope struct {
	WorkspaceID  string
	UseCase      string
	ResourceType string
	TargetID     string
	Key          string
}

func (scope TransactionIdempotencyScope) Normalized() TransactionIdempotencyScope {
	scope.WorkspaceID = strings.TrimSpace(scope.WorkspaceID)
	scope.UseCase = strings.TrimSpace(scope.UseCase)
	scope.ResourceType = strings.TrimSpace(scope.ResourceType)
	scope.TargetID = strings.TrimSpace(scope.TargetID)
	scope.Key = strings.TrimSpace(scope.Key)
	return scope
}

func (scope TransactionIdempotencyScope) IsComplete() bool {
	scope = scope.Normalized()
	return scope.WorkspaceID != "" && scope.UseCase != "" && scope.ResourceType != "" && scope.Key != ""
}

type TransactionRetryIdentity struct {
	IdempotencyScope TransactionIdempotencyScope
	CorrelationID    string
}

func (identity TransactionRetryIdentity) Normalized() TransactionRetryIdentity {
	identity.IdempotencyScope = identity.IdempotencyScope.Normalized()
	identity.CorrelationID = strings.TrimSpace(identity.CorrelationID)
	return identity
}

func (identity TransactionRetryIdentity) IsComplete() bool {
	identity = identity.Normalized()
	return identity.IdempotencyScope.IsComplete() && identity.CorrelationID != ""
}

func WithTransactionRetryIdentity(ctx context.Context, identity TransactionRetryIdentity) context.Context {
	if ctx == nil || !identity.IsComplete() {
		return ctx
	}
	return context.WithValue(ctx, transactionRetryIdentityContextKey{}, identity.Normalized())
}

func TransactionRetryIdentityFromContext(ctx context.Context) (TransactionRetryIdentity, bool) {
	if ctx == nil {
		return TransactionRetryIdentity{}, false
	}
	identity, ok := ctx.Value(transactionRetryIdentityContextKey{}).(TransactionRetryIdentity)
	identity = identity.Normalized()
	return identity, ok && identity.IsComplete()
}
