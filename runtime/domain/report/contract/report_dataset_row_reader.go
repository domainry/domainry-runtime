package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReportDatasetRowReadRequest struct {
	WorkspaceID string
	Plan        reportmodel.ReportDatasetPlan
	Objects     map[string]definitionmodel.ObjectSchema
	Queries     map[string]recordmodel.RecordListQuery
}

type ReportDatasetRecordRow struct {
	Records map[string]recordmodel.Record
}

// ReportDatasetRowReader pushes tenant/RLS filters, safe dataset filters,
// projections, and joins to the persistence engine while Report remains the
// owner of exact aggregation and analytics semantics.
type ReportDatasetRowReader interface {
	ReadReportDatasetRows(context.Context, ReportDatasetRowReadRequest) ([]ReportDatasetRecordRow, error)
}
