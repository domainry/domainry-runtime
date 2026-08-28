package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// ReportRecordReader is the minimal Record capability required to evaluate a report.
type ReportRecordReader interface {
	ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}

// ReportRecordAccess exposes the Record visibility rules needed by reporting without
// coupling Report to the Record service or repository packages.
type ReportRecordAccess interface {
	ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	NormalizeReportListQuery(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery
	CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
}

// ReportRecordFieldProjector is optional so Report remains usable with simple
// test/read adapters while production applies contextual CLS before metrics.
type ReportRecordFieldProjector interface {
	ProjectReportRecordFields(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, []recordmodel.Record) ([]recordmodel.Record, error)
}

// ReportDatasetPushdownAuthorizer lets the Record owner decide whether a
// dataset may bypass row-by-row field projection. Report must not duplicate
// baseline, masking, or contextual CLS semantics when selecting its SQL path.
type ReportDatasetPushdownAuthorizer interface {
	CanPushdownReportDataset(context.Context, principalmodel.Principal, []definitionmodel.ObjectSchema) bool
}

// ReportExportFieldAuthorizer lets the Record owner apply the full Object
// schema, guardrail, explicit sensitive-field, export, and masking policy.
type ReportExportFieldAuthorizer interface {
	AuthorizeReportExportField(context.Context, principalmodel.Principal, string, string) (masked bool, err error)
}

// ReportObjectSQLFieldAuthorizer is stricter than ordinary projection: an
// unreadable, masked, disabled, or contextual field must fail the entire SQL
// definition regardless of which clause references it.
type ReportObjectSQLFieldAuthorizer interface {
	AuthorizeReportObjectSQLField(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, string) error
}
