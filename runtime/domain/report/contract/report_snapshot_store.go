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
	LeaseOwner      string
	LeaseExpiresAt  string
}

type ReportSnapshotCompleteRequest struct {
	Snapshot       reportmodel.ReportSnapshot
	ExpectedStatus string
	LeaseOwner     string
	FencingToken   int64
}

type ReportSnapshotFailRequest struct {
	WorkspaceID    string
	ID             string
	ExpectedStatus string
	ErrorCode      string
	LeaseOwner     string
	FencingToken   int64
}

type ReportSnapshotClaimDisposition string

const (
	ReportSnapshotClaimAcquired ReportSnapshotClaimDisposition = "acquired"
	ReportSnapshotClaimRunning  ReportSnapshotClaimDisposition = "running"
	ReportSnapshotClaimReplay   ReportSnapshotClaimDisposition = "replay"
)

type ReportSnapshotClaim struct {
	Snapshot    reportmodel.ReportSnapshot
	Disposition ReportSnapshotClaimDisposition
}

func (c ReportSnapshotClaim) Acquired() bool {
	return c.Disposition == ReportSnapshotClaimAcquired
}

type ReportSnapshotStore interface {
	BeginReportSnapshot(context.Context, ReportSnapshotBeginRequest) (ReportSnapshotClaim, error)
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
