package contract

import (
	"context"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// RecordMutationExecutionStore owns Record command claims and the atomic
// mutation-plus-receipt commit. Scope derivation remains in the Record owner.
type RecordMutationExecutionStore interface {
	TryBeginRecordMutation(context.Context, recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error)
	CommitRecordMutationExecution(context.Context, transactionmodel.RecordMutationCommit, recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error)
	CompleteRecordMutationExecution(context.Context, recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error)
}
