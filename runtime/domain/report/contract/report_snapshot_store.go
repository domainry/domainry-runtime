package contract

import (
	"context"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type ReportSnapshotSourceVersionRequest struct {
	WorkspaceID string
	Objects     map[string]definitionmodel.ObjectSchema
	Queries     map[string]recordmodel.RecordListQuery
}

type ReportSnapshotSourceVersionReader interface {
	ReadReportSnapshotSourceVersion(context.Context, ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error)
}

// Analysis requires a content/revision fingerprint, including same-timestamp
// updates. Existing snapshot watermark readers remain source compatible.
type ReportAnalysisSourceVersionReader interface {
	ReadReportAnalysisSourceVersion(context.Context, ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error)
}
