package contract

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReportSnapshotBeginRequest struct {
	WorkspaceID     string
	ReportKey       string
	AccessScopeHash string
	IdempotencyKey  string
	StartedAt       string
}

type ReportSnapshotCompleteRequest struct {
	Snapshot       reportmodel.ReportSnapshot
	ExpectedStatus string
}

type ReportSnapshotFailRequest struct {
	WorkspaceID    string
	ID             string
	ExpectedStatus string
	ErrorCode      string
}

type ReportSnapshotStore interface {
	BeginReportSnapshot(context.Context, ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error)
	CompleteReportSnapshot(context.Context, ReportSnapshotCompleteRequest) error
	FailReportSnapshot(context.Context, ReportSnapshotFailRequest) error
	LatestReportSnapshot(context.Context, string, string, string) (reportmodel.ReportSnapshot, bool, error)
}

type ReportSnapshotSourceVersionRequest struct {
	WorkspaceID string
	Objects     map[string]definitionmodel.ObjectSchema
	Queries     map[string]recordmodel.RecordListQuery
}

type ReportSnapshotSourceVersionReader interface {
	ReadReportSnapshotSourceVersion(context.Context, ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error)
}
