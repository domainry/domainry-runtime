package query

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/domain/report/snapshot"
)

func (s *ReportDomainService) readReportSnapshot(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.snapshots.ReadSnapshot(ctx, report, principal)
}

func (s *ReportDomainService) RefreshSnapshot(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	return s.snapshots.RefreshSnapshot(ctx, reportKey, idempotencyKey, principal)
}

func (s *ReportDomainService) PrepareSnapshotRefresh(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportsnapshot.ReportSnapshotRefresh, error) {
	return s.snapshots.PrepareSnapshotRefresh(ctx, reportKey, idempotencyKey, principal)
}

func (s *ReportDomainService) CommitSnapshotRefresh(ctx context.Context, refresh reportsnapshot.ReportSnapshotRefresh) error {
	return s.snapshots.CommitSnapshotRefresh(ctx, refresh)
}

func ReportAccessScopeHash(principal principalmodel.Principal) (string, error) {
	return reportsnapshot.ReportAccessScopeHash(principal)
}
