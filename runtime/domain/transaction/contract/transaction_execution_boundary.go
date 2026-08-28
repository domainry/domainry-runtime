package contract

import "context"

type activeTransactionContextKey struct{}

func WithActiveTransaction(ctx context.Context) context.Context {
	return context.WithValue(ctx, activeTransactionContextKey{}, true)
}

func ActiveTransaction(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(activeTransactionContextKey{}).(bool)
	return active
}
