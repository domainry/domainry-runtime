package lifecycle

import (
	"context"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type lifecycleRepositoryProbe struct {
	savePolicy          func(context.Context, lifecyclemodel.PolicyVersion) error
	latestPolicy        func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error)
	listPolicies        func(context.Context, string) ([]lifecyclemodel.PolicyVersion, error)
	saveLegalHold       func(context.Context, lifecyclemodel.LegalHold) error
	getLegalHold        func(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error)
	activeLegalHolds    func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error)
	saveCleanupJob      func(context.Context, lifecyclemodel.CleanupJob) error
	getCleanupJob       func(context.Context, string, string) (lifecyclemodel.CleanupJob, bool, error)
	listRunnableJobs    func(context.Context, principalmodel.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error)
	claimCleanupJob     func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error)
	updateCleanupJob    func(context.Context, lifecyclemodel.CleanupJob) error
	saveSubjectRequest  func(context.Context, lifecyclemodel.SubjectRequest) error
	getSubjectRequest   func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error)
	expireSubjectExport func(context.Context, principalmodel.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error)
	saveExternal        func(context.Context, []lifecyclemodel.ExternalErasure) error
	listExternal        func(context.Context, string, string) ([]lifecyclemodel.ExternalErasure, error)
	reconcileExternal   func(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error)
	saveDeletion        func(context.Context, lifecyclemodel.DeletionRegistration) error
	listDeletions       func(context.Context, string, int) ([]lifecyclemodel.DeletionRegistration, error)
	listArchive         func(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error)
	appendAudit         func(context.Context, lifecyclemodel.AuditEvidence) error
	metrics             func(context.Context, string, time.Time) (lifecyclemodel.Metrics, error)
	globalMetrics       func(context.Context, principalmodel.SystemScope, time.Time) (lifecyclemodel.Metrics, error)
}

func (p *lifecycleRepositoryProbe) SavePolicy(ctx context.Context, value lifecyclemodel.PolicyVersion) error {
	if p.savePolicy != nil {
		return p.savePolicy(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) LatestPolicy(ctx context.Context, workspaceID, key string) (lifecyclemodel.PolicyVersion, bool, error) {
	if p.latestPolicy != nil {
		return p.latestPolicy(ctx, workspaceID, key)
	}
	return lifecyclemodel.PolicyVersion{}, false, nil
}
func (p *lifecycleRepositoryProbe) ListPolicies(ctx context.Context, workspaceID string) ([]lifecyclemodel.PolicyVersion, error) {
	if p.listPolicies != nil {
		return p.listPolicies(ctx, workspaceID)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) SaveLegalHold(ctx context.Context, value lifecyclemodel.LegalHold) error {
	if p.saveLegalHold != nil {
		return p.saveLegalHold(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) GetLegalHold(ctx context.Context, workspaceID, id string) (lifecyclemodel.LegalHold, bool, error) {
	if p.getLegalHold != nil {
		return p.getLegalHold(ctx, workspaceID, id)
	}
	return lifecyclemodel.LegalHold{}, false, nil
}
func (p *lifecycleRepositoryProbe) ActiveLegalHolds(ctx context.Context, target lifecyclemodel.ResourceTarget, now time.Time) ([]lifecyclemodel.LegalHold, error) {
	if p.activeLegalHolds != nil {
		return p.activeLegalHolds(ctx, target, now)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) SaveCleanupJob(ctx context.Context, value lifecyclemodel.CleanupJob) error {
	if p.saveCleanupJob != nil {
		return p.saveCleanupJob(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) GetCleanupJob(ctx context.Context, workspaceID, id string) (lifecyclemodel.CleanupJob, bool, error) {
	if p.getCleanupJob != nil {
		return p.getCleanupJob(ctx, workspaceID, id)
	}
	return lifecyclemodel.CleanupJob{}, false, nil
}
func (p *lifecycleRepositoryProbe) ListRunnableCleanupJobs(ctx context.Context, scope principalmodel.SystemScope, limit int, now time.Time) ([]lifecyclemodel.CleanupJob, error) {
	if p.listRunnableJobs != nil {
		return p.listRunnableJobs(ctx, scope, limit, now)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) ClaimCleanupJob(ctx context.Context, workspaceID, id, owner string, ttl time.Duration, now time.Time) (lifecyclemodel.CleanupJob, bool, error) {
	if p.claimCleanupJob != nil {
		return p.claimCleanupJob(ctx, workspaceID, id, owner, ttl, now)
	}
	return lifecyclemodel.CleanupJob{}, false, nil
}
func (p *lifecycleRepositoryProbe) UpdateCleanupJob(ctx context.Context, value lifecyclemodel.CleanupJob) error {
	if p.updateCleanupJob != nil {
		return p.updateCleanupJob(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) SaveSubjectRequest(ctx context.Context, value lifecyclemodel.SubjectRequest) error {
	if p.saveSubjectRequest != nil {
		return p.saveSubjectRequest(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) GetSubjectRequest(ctx context.Context, workspaceID, id string) (lifecyclemodel.SubjectRequest, bool, error) {
	if p.getSubjectRequest != nil {
		return p.getSubjectRequest(ctx, workspaceID, id)
	}
	return lifecyclemodel.SubjectRequest{}, false, nil
}
func (p *lifecycleRepositoryProbe) ExpireSubjectExportReferences(ctx context.Context, scope principalmodel.SystemScope, now time.Time) ([]lifecyclemodel.SubjectRequest, error) {
	if p.expireSubjectExport != nil {
		return p.expireSubjectExport(ctx, scope, now)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) SaveExternalErasures(ctx context.Context, values []lifecyclemodel.ExternalErasure) error {
	if p.saveExternal != nil {
		return p.saveExternal(ctx, values)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) ListExternalErasures(ctx context.Context, workspaceID, requestID string) ([]lifecyclemodel.ExternalErasure, error) {
	if p.listExternal != nil {
		return p.listExternal(ctx, workspaceID, requestID)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) ReconcileExternalErasure(ctx context.Context, workspaceID, id, evidence string, now time.Time) (lifecyclemodel.ExternalErasure, bool, error) {
	if p.reconcileExternal != nil {
		return p.reconcileExternal(ctx, workspaceID, id, evidence, now)
	}
	return lifecyclemodel.ExternalErasure{}, false, nil
}
func (p *lifecycleRepositoryProbe) SaveDeletionRegistration(ctx context.Context, value lifecyclemodel.DeletionRegistration) error {
	if p.saveDeletion != nil {
		return p.saveDeletion(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) ListPendingDeletionRegistrations(ctx context.Context, workspaceID string, limit int) ([]lifecyclemodel.DeletionRegistration, error) {
	if p.listDeletions != nil {
		return p.listDeletions(ctx, workspaceID, limit)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) ListArchiveEntries(ctx context.Context, workspaceID, sourceTable string, limit int) ([]lifecyclemodel.ArchiveEntry, error) {
	if p.listArchive != nil {
		return p.listArchive(ctx, workspaceID, sourceTable, limit)
	}
	return nil, nil
}
func (p *lifecycleRepositoryProbe) AppendAuditEvidence(ctx context.Context, value lifecyclemodel.AuditEvidence) error {
	if p.appendAudit != nil {
		return p.appendAudit(ctx, value)
	}
	return nil
}
func (p *lifecycleRepositoryProbe) Metrics(ctx context.Context, workspaceID string, now time.Time) (lifecyclemodel.Metrics, error) {
	if p.metrics != nil {
		return p.metrics(ctx, workspaceID, now)
	}
	return lifecyclemodel.Metrics{}, nil
}
func (p *lifecycleRepositoryProbe) GlobalMetrics(ctx context.Context, scope principalmodel.SystemScope, now time.Time) (lifecyclemodel.Metrics, error) {
	if p.globalMetrics != nil {
		return p.globalMetrics(ctx, scope, now)
	}
	return lifecyclemodel.Metrics{}, nil
}
