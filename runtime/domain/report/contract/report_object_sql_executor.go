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
	// PageOffset and PageSize are Runtime-owned execution controls. They are
	// never interpolated from author SQL. A positive PageSize makes the store
	// fetch at most PageSize+1 rows so callers can derive has-more without
	// materializing the complete Report result.
	PageOffset int
	PageSize   int
}

type ReportObjectSQLExecutionResult struct {
	Rows       []map[string]string
	HasMore    bool
	Total      int
	TotalKnown bool
}

// ReportObjectSQLExecutor executes only an already parsed, metadata-bound,
// permission-authorized plan. It never receives author SQL text.
type ReportObjectSQLExecutor interface {
	ExecuteReportObjectSQL(context.Context, ReportObjectSQLExecutionRequest) (ReportObjectSQLExecutionResult, error)
}
