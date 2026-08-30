package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// ApplicationSchemaReportEvidenceReader is the minimum live-record query boundary used by report definition validation.
type ApplicationSchemaReportEvidenceReader interface {
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}
