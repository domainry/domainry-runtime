package report

import (
	"context"
	"encoding/json"
	"fmt"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

// ModuleReportSnapshotStore is a protocol adapter only. Report owns all
// snapshot DML in domainry-report; Runtime maps its application model to the
// deployment-neutral SDK while it still owns cross-owner orchestration.
type ModuleReportSnapshotStore struct {
	repository reportpersistence.SnapshotRepository
}

func NewModuleReportSnapshotStore(repository reportpersistence.SnapshotRepository) *ModuleReportSnapshotStore {
	return &ModuleReportSnapshotStore{repository: repository}
}

func (s *ModuleReportSnapshotStore) BeginReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotBeginRequest) (reportcontract.ReportSnapshotClaim, error) {
	claim, err := s.repository.Claim(ctx, reportpersistence.SnapshotBeginRequest{WorkspaceID: request.WorkspaceID, ReportKey: request.ReportKey, AccessScopeHash: request.AccessScopeHash, IdempotencyKey: request.IdempotencyKey, StartedAt: request.StartedAt, LeaseOwner: request.LeaseOwner, LeaseExpiresAt: request.LeaseExpiresAt})
	if err != nil {
		return reportcontract.ReportSnapshotClaim{}, err
	}
	snapshot, err := reportSnapshotFromSDK(claim.Snapshot)
	return reportcontract.ReportSnapshotClaim{Snapshot: snapshot, Disposition: reportcontract.ReportSnapshotClaimDisposition(claim.Disposition)}, err
}

func (s *ModuleReportSnapshotStore) CompleteReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotCompleteRequest) error {
	snapshot, err := reportSnapshotToSDK(request.Snapshot)
	if err != nil {
		return err
	}
	return s.repository.Complete(ctx, reportpersistence.SnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: request.ExpectedStatus, LeaseOwner: request.LeaseOwner, FencingToken: request.FencingToken})
}

func (s *ModuleReportSnapshotStore) FailReportSnapshot(ctx context.Context, request reportcontract.ReportSnapshotFailRequest) error {
	return s.repository.Fail(ctx, reportpersistence.SnapshotFailRequest{WorkspaceID: request.WorkspaceID, ID: request.ID, ExpectedStatus: request.ExpectedStatus, ErrorCode: request.ErrorCode, LeaseOwner: request.LeaseOwner, FencingToken: request.FencingToken})
}

func (s *ModuleReportSnapshotStore) LatestReportSnapshot(ctx context.Context, workspaceID, reportKey, scopeHash string) (reportmodel.ReportSnapshot, bool, error) {
	snapshot, found, err := s.repository.Latest(ctx, workspaceID, reportKey, scopeHash)
	if err != nil || !found {
		return reportmodel.ReportSnapshot{}, found, err
	}
	value, err := reportSnapshotFromSDK(snapshot)
	return value, true, err
}

func reportSnapshotToSDK(snapshot reportmodel.ReportSnapshot) (reportpersistence.Snapshot, error) {
	summary, err := json.Marshal(snapshot.Summary)
	if err != nil {
		return reportpersistence.Snapshot{}, fmt.Errorf("encode Report snapshot summary: %w", err)
	}
	return reportpersistence.Snapshot{ID: snapshot.ID, WorkspaceID: snapshot.WorkspaceID, ReportKey: snapshot.ReportKey, AccessScopeHash: snapshot.AccessScopeHash, IdempotencyKey: snapshot.IdempotencyKey, Status: snapshot.Status, Summary: summary, Watermark: snapshot.Watermark, SourceVersions: snapshot.SourceVersions, StartedAt: snapshot.StartedAt, RefreshedAt: snapshot.RefreshedAt, ErrorCode: snapshot.ErrorCode, LeaseOwner: snapshot.LeaseOwner, LeaseExpiresAt: snapshot.LeaseExpiresAt, FencingToken: snapshot.FencingToken}, nil
}

func reportSnapshotFromSDK(snapshot reportpersistence.Snapshot) (reportmodel.ReportSnapshot, error) {
	value := reportmodel.ReportSnapshot{ID: snapshot.ID, WorkspaceID: snapshot.WorkspaceID, ReportKey: snapshot.ReportKey, AccessScopeHash: snapshot.AccessScopeHash, IdempotencyKey: snapshot.IdempotencyKey, Status: snapshot.Status, Watermark: snapshot.Watermark, SourceVersions: snapshot.SourceVersions, StartedAt: snapshot.StartedAt, RefreshedAt: snapshot.RefreshedAt, ErrorCode: snapshot.ErrorCode, LeaseOwner: snapshot.LeaseOwner, LeaseExpiresAt: snapshot.LeaseExpiresAt, FencingToken: snapshot.FencingToken}
	if len(snapshot.Summary) > 0 {
		if err := json.Unmarshal(snapshot.Summary, &value.Summary); err != nil {
			return reportmodel.ReportSnapshot{}, fmt.Errorf("decode Report snapshot summary: %w", err)
		}
	}
	return value, nil
}
