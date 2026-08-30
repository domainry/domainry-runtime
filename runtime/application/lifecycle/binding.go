// Package lifecycle is Runtime's compatibility binding for the reusable
// Lifecycle module. It translates Runtime principal/scope values only; all
// Lifecycle use cases execute in github.com/domainry/domainry-lifecycle.
package lifecycle

import (
	"context"
	"encoding/json"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle/access"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/application"
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	PermissionPolicyManage  = lifecycleapplication.PermissionPolicyManage
	PermissionCleanupRun    = lifecycleapplication.PermissionCleanupRun
	PermissionSubjectManage = lifecycleapplication.PermissionSubjectManage
)

type LifecycleApplicationDependencies = lifecycleapplication.LifecycleApplicationDependencies

type LifecycleApplicationService struct {
	core *lifecycleapplication.LifecycleApplicationService
}

func NewLifecycleApplicationService(ctx context.Context, deps LifecycleApplicationDependencies) *LifecycleApplicationService {
	return &LifecycleApplicationService{core: lifecycleapplication.NewLifecycleApplicationService(ctx, deps)}
}

func runtimePrincipal(value principalmodel.Principal) lifecycleaccess.Principal {
	permissions := value.PermissionKeys()
	grants := make(map[string]struct{}, len(permissions)+3)
	for _, permission := range permissions {
		grants[permission] = struct{}{}
	}
	for _, permission := range []string{PermissionPolicyManage, PermissionCleanupRun, PermissionSubjectManage} {
		if value.HasPermission(permission) {
			grants[permission] = struct{}{}
		}
	}
	return lifecycleaccess.Principal{UserID: value.UserID, WorkspaceID: value.WorkspaceID, Known: value.Known, SystemScope: runtimeSystemScope(value.SystemScope), Permissions: grants}
}

func runtimeSystemScope(value principalmodel.SystemScope) lifecycleaccess.SystemScope {
	kind := lifecycleaccess.SystemScopeGlobal
	switch value.Kind {
	case principalmodel.SystemScopeBootstrap:
		kind = lifecycleaccess.SystemScopeBootstrap
	case principalmodel.SystemScopeInstallation:
		kind = lifecycleaccess.SystemScopeInstallation
	}
	return lifecycleaccess.NewSystemScope(kind, value.Purpose)
}

func (s *LifecycleApplicationService) PublishPolicy(ctx context.Context, value lifecyclemodel.PolicyVersion, principal principalmodel.Principal) (lifecyclemodel.PolicyVersion, error) {
	return s.core.PublishPolicy(ctx, value, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ListPolicies(ctx context.Context, principal principalmodel.Principal) ([]lifecyclemodel.PolicyVersion, error) {
	return s.core.ListPolicies(ctx, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) CreateLegalHold(ctx context.Context, value lifecyclemodel.LegalHold, principal principalmodel.Principal) (lifecyclemodel.LegalHold, error) {
	return s.core.CreateLegalHold(ctx, value, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) EndLegalHold(ctx context.Context, workspaceID, holdID, authority, evidence string, endedAt time.Time, principal principalmodel.Principal) (lifecyclemodel.LegalHold, error) {
	return s.core.EndLegalHold(ctx, workspaceID, holdID, authority, evidence, endedAt, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) PreviewCleanup(ctx context.Context, workspaceID, policyKey string, principal principalmodel.Principal, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	return s.core.PreviewCleanup(ctx, workspaceID, policyKey, runtimePrincipal(principal), now)
}
func (s *LifecycleApplicationService) CreateCleanupJob(ctx context.Context, value lifecyclemodel.CleanupJob, principal principalmodel.Principal) (lifecyclemodel.CleanupJob, error) {
	return s.core.CreateCleanupJob(ctx, value, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ProcessCleanupJob(ctx context.Context, workspaceID, jobID, leaseOwner string, leaseTTL time.Duration, batchSize int, now time.Time, principal principalmodel.Principal) (lifecyclemodel.CleanupJob, error) {
	return s.core.ProcessCleanupJob(ctx, workspaceID, jobID, leaseOwner, leaseTTL, batchSize, now, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) CreateSubjectRequest(ctx context.Context, value lifecyclemodel.SubjectRequest, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	return s.core.CreateSubjectRequest(ctx, value, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) VerifySubjectRequest(ctx context.Context, workspaceID, requestID, secondFactor string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	return s.core.VerifySubjectRequest(ctx, workspaceID, requestID, secondFactor, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) PreviewSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	return s.core.PreviewSubjectRequest(ctx, workspaceID, requestID, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ApproveSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	return s.core.ApproveSubjectRequest(ctx, workspaceID, requestID, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ExecuteSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	return s.core.ExecuteSubjectRequest(ctx, workspaceID, requestID, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) Metrics(ctx context.Context, principal principalmodel.Principal, now time.Time) (lifecyclemodel.Metrics, error) {
	return s.core.Metrics(ctx, runtimePrincipal(principal), now)
}
func (s *LifecycleApplicationService) HealthForSystem(ctx context.Context, scope principalmodel.SystemScope, now time.Time) (map[string]any, error) {
	return s.core.HealthForSystem(ctx, runtimeSystemScope(scope), now)
}
func (s *LifecycleApplicationService) ListArchiveEntries(ctx context.Context, sourceTable string, limit int, principal principalmodel.Principal) ([]lifecyclemodel.ArchiveEntry, error) {
	return s.core.ListArchiveEntries(ctx, sourceTable, limit, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ListExternalErasures(ctx context.Context, requestID string, principal principalmodel.Principal) ([]lifecyclemodel.ExternalErasure, error) {
	return s.core.ListExternalErasures(ctx, requestID, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) ReconcileExternalErasure(ctx context.Context, id, evidence string, principal principalmodel.Principal, now time.Time) (lifecyclemodel.ExternalErasure, error) {
	return s.core.ReconcileExternalErasure(ctx, id, evidence, runtimePrincipal(principal), now)
}
func (s *LifecycleApplicationService) DownloadSubjectExport(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal, now time.Time) (json.RawMessage, error) {
	return s.core.DownloadSubjectExport(ctx, workspaceID, requestID, runtimePrincipal(principal), now)
}
func (s *LifecycleApplicationService) InstallDefaultPolicies(ctx context.Context, workspaceID string, principal principalmodel.Principal, now time.Time) error {
	return s.core.InstallDefaultPolicies(ctx, workspaceID, runtimePrincipal(principal), now)
}
func (s *LifecycleApplicationService) ProcessRunnableCleanupJobs(ctx context.Context, leaseOwner string, batchSize, jobLimit int, now time.Time, scope principalmodel.SystemScope) (int, error) {
	return s.core.ProcessRunnableCleanupJobs(ctx, leaseOwner, batchSize, jobLimit, now, runtimeSystemScope(scope))
}
func (s *LifecycleApplicationService) ReplayRegisteredDeletions(ctx context.Context, workspaceID string, limit int, principal principalmodel.Principal) (int, error) {
	return s.core.ReplayRegisteredDeletions(ctx, workspaceID, limit, runtimePrincipal(principal))
}
func (s *LifecycleApplicationService) CleanupExpiredSubjectArtifacts(ctx context.Context, now time.Time, scope principalmodel.SystemScope) (int, error) {
	return s.core.CleanupExpiredSubjectArtifacts(ctx, now, runtimeSystemScope(scope))
}

type WorkerTick struct {
	LeaseOwner string
	BatchSize  int
	JobLimit   int
	Now        time.Time
	Scope      principalmodel.SystemScope
}
type WorkerTickResult = lifecycleapplication.WorkerTickResult
type WorkerRunner struct {
	core *lifecycleapplication.WorkerRunner
}

func NewWorkerRunner(service *LifecycleApplicationService) *WorkerRunner {
	if service == nil {
		return &WorkerRunner{}
	}
	return &WorkerRunner{core: lifecycleapplication.NewWorkerRunner(service.core)}
}
func (r *WorkerRunner) Tick(ctx context.Context, tick WorkerTick) (WorkerTickResult, error) {
	if r == nil || r.core == nil {
		return lifecycleapplication.NewWorkerRunner(nil).Tick(ctx, lifecycleapplication.WorkerTick{})
	}
	return r.core.Tick(ctx, lifecycleapplication.WorkerTick{LeaseOwner: tick.LeaseOwner, BatchSize: tick.BatchSize, JobLimit: tick.JobLimit, Now: tick.Now, Scope: runtimeSystemScope(tick.Scope)})
}
