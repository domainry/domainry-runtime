package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// ActionRecordReader is the minimal aggregate read capability required by
// Action preconditions and settlement policies.
type ActionRecordReader interface {
	GetActionRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	ListActionRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}
