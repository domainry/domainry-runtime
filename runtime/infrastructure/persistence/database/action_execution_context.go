package database

import (
	"context"
	"database/sql"
)

type actionExecutionTransactionContextKey struct{}

// ActionExecutionExecutor is the adapter-private SQL contract shared by
// persistence collaborators inside one Runtime Action transaction. It omits
// lifecycle methods so callers cannot commit or roll back the transaction.
type ActionExecutionExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func WithActionExecutionTransaction(
	ctx context.Context,
	executor ActionExecutionExecutor,
) context.Context {
	if ctx == nil || executor == nil {
		return ctx
	}
	return context.WithValue(ctx, actionExecutionTransactionContextKey{}, executor)
}

func ActionExecutionTransaction(ctx context.Context) ActionExecutionExecutor {
	if ctx == nil {
		return nil
	}
	executor, _ := ctx.Value(actionExecutionTransactionContextKey{}).(ActionExecutionExecutor)
	return executor
}
