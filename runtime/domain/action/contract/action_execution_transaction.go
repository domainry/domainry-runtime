package contract

import (
	"context"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// ActionExecutionTransaction is the narrow Runtime-owned transaction session
// used after an Action first requests a mutation, CAS, or locking read. The
// storage transaction itself remains adapter-private.
type ActionExecutionTransaction interface {
	Context(context.Context) context.Context
	Commit(context.Context, []transactionmodel.RecordMutationCommit, actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error)
	RollBack(context.Context) error
}

// ActionExecutionTransactionStore lazily opens an Action transaction without
// exposing database handles to Application or project code.
type ActionExecutionTransactionStore interface {
	BeginExecutionTransaction(context.Context) (ActionExecutionTransaction, error)
}
