package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	lifecyclepolicy "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	requestcontext "github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func (s *LifecycleApplicationService) policyExecutor(ctx context.Context, workspaceID, policyKey string) (lifecyclemodel.PolicyVersion, lifecyclecontract.OwnerLifecycleExecutor, error) {
	version, found, err := s.repository.LatestPolicy(ctx, workspaceID, policyKey)
	if err != nil || !found {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle policy not found: %s", policyKey)
	}
	executor := s.executors[version.Policy.Owner]
	if executor == nil {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle owner executor unavailable: %s", version.Policy.Owner)
	}
	return version, executor, nil
}

func (s *LifecycleApplicationService) subjectRequest(ctx context.Context, workspaceID, requestID string) (lifecyclemodel.SubjectRequest, error) {
	request, found, err := s.repository.GetSubjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if !found {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject request not found")
	}
	return request, nil
}

func (s *LifecycleApplicationService) transitionSubject(ctx context.Context, current, next lifecyclemodel.SubjectRequest, actor, event string) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecyclepolicy.TransitionSubjectRequest(current, next); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	type transitionRepository interface {
		TransitionSubjectRequest(context.Context, lifecyclemodel.SubjectRequest, lifecyclemodel.SubjectRequest) error
	}
	var err error
	if repository, ok := s.repository.(transitionRepository); ok {
		err = repository.TransitionSubjectRequest(ctx, current, next)
	} else {
		err = s.repository.SaveSubjectRequest(ctx, next)
	}
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	return next, s.audit(ctx, next.WorkspaceID, event, actor, next.ID, "", next)
}

func (s *LifecycleApplicationService) failCleanup(ctx context.Context, job lifecyclemodel.CleanupJob, cause error, now time.Time, actor string) (lifecyclemodel.CleanupJob, error) {
	job.Status, job.LastError, job.LeaseOwner, job.LeaseExpiresAt, job.UpdatedAt = lifecyclemodel.CleanupStatusFailed, cause.Error(), "", time.Time{}, now
	if err := s.repository.UpdateCleanupJob(ctx, job); err != nil {
		return job, err
	}
	if err := s.audit(ctx, job.WorkspaceID, "lifecycle.cleanup.failed", actor, job.ID, job.PolicyKey, job); err != nil {
		return job, err
	}
	return job, cause
}

func (s *LifecycleApplicationService) audit(ctx context.Context, workspaceID, event, actor, resourceID, policyKey string, payload any) error {
	raw, _ := json.Marshal(payload)
	return s.repository.AppendAuditEvidence(ctx, lifecyclemodel.AuditEvidence{ID: requestcontext.NewRequestID(), WorkspaceID: workspaceID, Event: event, ActorID: actor, ResourceID: resourceID, PolicyKey: policyKey, Payload: raw, CreatedAt: time.Now().UTC()})
}

func lifecycleAuthorize(principal principalmodel.Principal, permission string) error {
	if _, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); !principal.Known || err != nil || !principal.HasPermission(permission) {
		return fmt.Errorf("auth.permission_denied")
	}
	return nil
}

func lifecycleAuthorizeWorkspace(principal principalmodel.Principal, workspaceID, permission string) error {
	if err := lifecycleAuthorize(principal, permission); err != nil {
		return err
	}
	if strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return fmt.Errorf("lifecycle workspace scope mismatch")
	}
	return nil
}

func lifecycleAuthorizeWorkspaceOrSystem(principal principalmodel.Principal, workspaceID, permission string) error {
	if principal.Known && principal.SystemScope.Valid() {
		return nil
	}
	return lifecycleAuthorizeWorkspace(principal, workspaceID, permission)
}
