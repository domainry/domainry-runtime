package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// PipelineRecordReader is the consumer-owned record query boundary required by Pipeline.
type PipelineRecordReader interface {
	GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}
