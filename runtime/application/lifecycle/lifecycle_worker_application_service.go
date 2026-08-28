package lifecycle

import (
	"context"
	"fmt"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *LifecycleApplicationService) ProcessRunnableCleanupJobs(ctx context.Context, leaseOwner string, batchSize, jobLimit int, now time.Time, scope principalmodel.SystemScope) (int, error) {
	principal := principalmodel.NewSystemPrincipal(leaseOwner, scope)
	jobs, err := s.repository.ListRunnableCleanupJobs(ctx, scope, jobLimit, now)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if _, err := s.ProcessCleanupJob(ctx, job.WorkspaceID, job.ID, leaseOwner, 2*time.Minute, batchSize, now, principal); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// ReplayRegisteredDeletions is a restore gate: a restored workspace must run
// its durable deletion registry before it can be exposed for normal traffic.
func (s *LifecycleApplicationService) ReplayRegisteredDeletions(ctx context.Context, workspaceID string, limit int, principal principalmodel.Principal) (int, error) {
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionSubjectManage); err != nil {
		return 0, err
	}
	registrations, err := s.repository.ListPendingDeletionRegistrations(ctx, workspaceID, limit)
	if err != nil {
		return 0, err
	}
	replayed := 0
	for _, registration := range registrations {
		holds, holdErr := s.repository.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, ResourceType: "data_subject", ResourceID: registration.ResolvedIdentity}, time.Now().UTC())
		if holdErr != nil {
			return replayed, holdErr
		}
		if len(holds) > 0 {
			return replayed, fmt.Errorf("backup deletion replay blocked by legal hold")
		}
		for _, handler := range s.subjectHandlers {
			if _, err := handler.EraseSubject(ctx, workspaceID, registration.ResolvedIdentity, nil); err != nil {
				return replayed, err
			}
		}
		replayed++
		if err := s.audit(ctx, workspaceID, "lifecycle.deletion.replayed", principal.UserID, registration.RequestID, "", registration); err != nil {
			return replayed, err
		}
	}
	return replayed, nil
}

func (s *LifecycleApplicationService) CleanupExpiredSubjectArtifacts(ctx context.Context, now time.Time, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, err
	}
	if s.artifacts == nil {
		return 0, nil
	}
	deleted, err := s.artifacts.DeleteExpiredSubjectExports(ctx, now)
	if err != nil {
		return deleted, err
	}
	stagingDeleted, err := s.artifacts.DeleteExpiredUploadStaging(ctx, now)
	deleted += stagingDeleted
	if err != nil {
		return deleted, err
	}
	if s.uploadArtifacts != nil {
		result, reconcileErr := s.uploadArtifacts.ReconcileUploadArtifacts(ctx, now, 500)
		deleted += result.Deleted
		if reconcileErr != nil {
			return deleted, reconcileErr
		}
	}
	expired, err := s.repository.ExpireSubjectExportReferences(ctx, scope, now)
	if err != nil {
		return deleted, err
	}
	for _, request := range expired {
		if err := s.audit(ctx, request.WorkspaceID, "lifecycle.subject.export_expired", "lifecycle_cleanup", request.ID, "", map[string]any{"request_id": request.ID}); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
