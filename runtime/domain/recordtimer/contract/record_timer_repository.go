package contract

import (
	"context"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// RecordTimerRepository is the persistence boundary for Runtime-owned,
// record-scoped delayed execution.
type RecordTimerRepository interface {
	GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error)
	CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error
}

type RecordTimerWorkspaceRepository interface {
	ListDueRecordTimerWorkspaces(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error)
}
