package contract

import (
	"context"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReportObjectSQLExecutionRequest struct {
	WorkspaceID string
	Plan        reportmodel.ReportObjectSQLPlan
	Objects     map[string]definitionmodel.ObjectSchema
	Queries     map[string]recordmodel.RecordListQuery
	Parameters  map[string]any
	Timeout     time.Duration
	// PageCursor is the opaque persistence cursor returned by the previous
	// execution. PagePosition fences the authored LIMIT without rescanning or
	// exposing cursor values above the persistence boundary.
	PageCursor   string
	PagePosition int
	PageSize     int
}

type ReportObjectSQLExecutionResult struct {
	Rows       []map[string]string
	HasMore    bool
	Total      int
	TotalKnown bool
	NextCursor string
}

// ReportObjectSQLExecutor executes only an already parsed, metadata-bound,
// permission-authorized plan. It never receives author SQL text.
type ReportObjectSQLExecutor interface {
	ExecuteReportObjectSQL(context.Context, ReportObjectSQLExecutionRequest) (ReportObjectSQLExecutionResult, error)
}
