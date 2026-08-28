package record

import (
	"context"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// TransactionExecutor is the adapter-private SQL surface shared by Record
// persistence and the Runtime Action transaction owner. It deliberately omits
// transaction lifecycle methods so Domain/Application code cannot commit it.
type TransactionExecutor = database.ActionExecutionExecutor

// WithActionExecutionTransaction binds the adapter-private SQL executor to
// Record persistence calls made by the same Runtime Action execution.
func WithActionExecutionTransaction(ctx context.Context, executor TransactionExecutor) context.Context {
	return database.WithActionExecutionTransaction(ctx, executor)
}

func actionExecutionTransaction(ctx context.Context) TransactionExecutor {
	return database.ActionExecutionTransaction(ctx)
}

// ActionExecutionTransaction returns the current adapter-private executor for
// Runtime persistence collaborators that must validate data inside the same
// Action transaction. It does not expose transaction lifecycle authority.
func ActionExecutionTransaction(ctx context.Context) TransactionExecutor {
	return database.ActionExecutionTransaction(ctx)
}
