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
	lifecyclerepository "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	requestcontext "github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

const (
	PermissionPolicyManage  = "lifecycle.policy.manage"
	PermissionCleanupRun    = "lifecycle.cleanup.run"
	PermissionSubjectManage = "lifecycle.subject.manage"
)

type LifecycleApplicationDependencies struct {
	Repository      lifecyclerepository.LifecycleRepository
	Executors       []lifecyclecontract.OwnerLifecycleExecutor
	SubjectResolver lifecyclecontract.SubjectIdentityResolver
	SubjectHandlers []lifecyclecontract.SubjectDataHandler
	ExternalErasure lifecyclecontract.ExternalErasureHandler
	Artifacts       lifecyclecontract.SubjectArtifactStore
	UploadArtifacts lifecyclecontract.UploadArtifactStore
}

type LifecycleApplicationService struct {
	repository      lifecyclerepository.LifecycleRepository
	executors       map[string]lifecyclecontract.OwnerLifecycleExecutor
	resolver        lifecyclecontract.SubjectIdentityResolver
	subjectHandlers []lifecyclecontract.SubjectDataHandler
	externalErasure lifecyclecontract.ExternalErasureHandler
	artifacts       lifecyclecontract.SubjectArtifactStore
	uploadArtifacts lifecyclecontract.UploadArtifactStore
}

func NewLifecycleApplicationService(ctx context.Context, deps LifecycleApplicationDependencies) *LifecycleApplicationService {
	service := &LifecycleApplicationService{repository: deps.Repository, executors: map[string]lifecyclecontract.OwnerLifecycleExecutor{}, resolver: deps.SubjectResolver, subjectHandlers: append([]lifecyclecontract.SubjectDataHandler(nil), deps.SubjectHandlers...), externalErasure: deps.ExternalErasure, artifacts: deps.Artifacts, uploadArtifacts: deps.UploadArtifacts}
	for _, executor := range deps.Executors {
		if executor != nil && strings.TrimSpace(executor.Owner(ctx)) != "" {
			service.executors[strings.TrimSpace(executor.Owner(ctx))] = executor
		}
	}
	return service
}

func (s *LifecycleApplicationService) PublishPolicy(ctx context.Context, version lifecyclemodel.PolicyVersion, principal principalmodel.Principal) (lifecyclemodel.PolicyVersion, error) {
	if err := lifecycleAuthorize(principal, PermissionPolicyManage); err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	if s == nil || s.repository == nil {
		return lifecyclemodel.PolicyVersion{}, fmt.Errorf("lifecycle repository unavailable")
	}
	version.PublishedBy = principal.UserID
	version.WorkspaceID = principal.WorkspaceID
	previous, found, err := s.repository.LatestPolicy(ctx, principal.WorkspaceID, version.Policy.Key)
	if err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	if found {
		if err := lifecyclepolicy.ValidatePolicyPublication(&previous, version); err != nil {
			return lifecyclemodel.PolicyVersion{}, err
		}
	} else if err := lifecyclepolicy.ValidatePolicyPublication(nil, version); err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	if err := s.repository.SavePolicy(ctx, version); err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	return version, s.audit(ctx, principal.WorkspaceID, "lifecycle.policy.published", principal.UserID, version.Policy.Key, version.Policy.Key, version)
}

func (s *LifecycleApplicationService) ListPolicies(ctx context.Context, principal principalmodel.Principal) ([]lifecyclemodel.PolicyVersion, error) {
	if err := lifecycleAuthorize(principal, PermissionPolicyManage); err != nil {
		return nil, err
	}
	return s.repository.ListPolicies(ctx, principal.WorkspaceID)
}

func (s *LifecycleApplicationService) CreateLegalHold(ctx context.Context, hold lifecyclemodel.LegalHold, principal principalmodel.Principal) (lifecyclemodel.LegalHold, error) {
	if err := lifecycleAuthorize(principal, PermissionPolicyManage); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	if hold.WorkspaceID != principal.WorkspaceID {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("lifecycle workspace scope mismatch")
	}
	if hold.ID == "" {
		hold.ID = requestcontext.NewRequestID()
	}
	if err := lifecyclepolicy.ValidateLegalHold(hold); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	if err := s.repository.SaveLegalHold(ctx, hold); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	return hold, s.audit(ctx, hold.WorkspaceID, "lifecycle.legal_hold.created", principal.UserID, hold.ID, "", hold)
}

func (s *LifecycleApplicationService) EndLegalHold(ctx context.Context, workspaceID, holdID, authority, evidence string, endedAt time.Time, principal principalmodel.Principal) (lifecyclemodel.LegalHold, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, PermissionPolicyManage); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	hold, found, err := s.repository.GetLegalHold(ctx, workspaceID, holdID)
	if err != nil || !found {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("legal hold not found")
	}
	if strings.TrimSpace(authority) == "" || strings.TrimSpace(evidence) == "" || endedAt.IsZero() || !endedAt.After(hold.StartsAt) {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("legal hold release authority, evidence and valid end are required")
	}
	hold.Authority, hold.AuditEvidence, hold.EndsAt = strings.TrimSpace(authority), strings.TrimSpace(evidence), &endedAt
	if err := s.repository.SaveLegalHold(ctx, hold); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	return hold, s.audit(ctx, workspaceID, "lifecycle.legal_hold.ended", principal.UserID, hold.ID, "", hold)
}

func (s *LifecycleApplicationService) PreviewCleanup(ctx context.Context, workspaceID, policyKey string, principal principalmodel.Principal, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, PermissionCleanupRun); err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, workspaceID, policyKey)
	if err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	return executor.Preview(ctx, workspaceID, policyVersion, now)
}

func (s *LifecycleApplicationService) CreateCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob, principal principalmodel.Principal) (lifecyclemodel.CleanupJob, error) {
	if err := lifecycleAuthorizeWorkspace(principal, job.WorkspaceID, PermissionCleanupRun); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, job.WorkspaceID, job.PolicyKey)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	if job.ID == "" {
		job.ID = requestcontext.NewRequestID()
	}
	job.PolicyVersion = policyVersion.Policy.Version
	job.RequestedBy = principal.UserID
	if job.Status == "" {
		job.Status = lifecyclemodel.CleanupStatusPending
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.UpdatedAt = job.CreatedAt
	preview, err := executor.Preview(ctx, job.WorkspaceID, policyVersion, job.CreatedAt)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	job.EstimatedRows, job.EstimatedBytes, job.OldestEligible = preview.Rows, preview.Bytes, preview.OldestEligible
	if err := lifecyclepolicy.ValidateCleanupJob(job); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	if err := s.repository.SaveCleanupJob(ctx, job); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	return job, s.audit(ctx, job.WorkspaceID, "lifecycle.cleanup.created", principal.UserID, job.ID, job.PolicyKey, job)
}

func (s *LifecycleApplicationService) ProcessCleanupJob(ctx context.Context, workspaceID, jobID, leaseOwner string, leaseTTL time.Duration, batchSize int, now time.Time, principal principalmodel.Principal) (lifecyclemodel.CleanupJob, error) {
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionCleanupRun); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	claimed, acquired, err := s.repository.ClaimCleanupJob(ctx, workspaceID, jobID, leaseOwner, leaseTTL, now)
	if err != nil || !acquired {
		return claimed, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, workspaceID, claimed.PolicyKey)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	holds, err := s.repository.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, Owner: policyVersion.Policy.Owner, ResourceID: "*"}, now)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	result, err := executor.ProcessBatch(ctx, claimed, policyVersion, holds, batchSize)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	claimed.Checkpoint = result.Checkpoint
	claimed.Scanned += result.Scanned
	claimed.Archived += result.Archived
	claimed.Purged += result.Purged
	claimed.Skipped += result.Skipped
	claimed.Failed += result.Failed
	claimed.OldestEligible = result.OldestEligible
	claimed.UpdatedAt = now
	claimed.LeaseOwner, claimed.LeaseExpiresAt = "", time.Time{}
	if result.Done || claimed.DryRun {
		claimed.Status = lifecyclemodel.CleanupStatusSucceeded
	} else {
		claimed.Status = lifecyclemodel.CleanupStatusPending
	}
	if err := s.repository.UpdateCleanupJob(ctx, claimed); err != nil {
		return claimed, err
	}
	event := "lifecycle.cleanup.progressed"
	if claimed.Status == lifecyclemodel.CleanupStatusSucceeded {
		event = "lifecycle.cleanup.succeeded"
	}
	return claimed, s.audit(ctx, workspaceID, event, principal.UserID, claimed.ID, claimed.PolicyKey, claimed)
}

func (s *LifecycleApplicationService) CreateSubjectRequest(ctx context.Context, request lifecyclemodel.SubjectRequest, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, request.WorkspaceID, PermissionSubjectManage); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if request.ID == "" {
		request.ID = requestcontext.NewRequestID()
	}
	request.RequestedBy, request.Status = principal.UserID, lifecyclemodel.SubjectRequestPendingVerification
	request.CreatedAt, request.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if request.Kind != lifecyclemodel.SubjectRequestExport && request.Kind != lifecyclemodel.SubjectRequestErase {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("unsupported subject request kind")
	}
	if request.SubjectID == "" || request.SubjectType == "" || request.Reason == "" {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject identity and reason are required")
	}
	if err := s.repository.SaveSubjectRequest(ctx, request); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	return request, s.audit(ctx, request.WorkspaceID, "lifecycle.subject.requested", principal.UserID, request.ID, "", request)
}

func (s *LifecycleApplicationService) VerifySubjectRequest(ctx context.Context, workspaceID, requestID, secondFactor string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, PermissionSubjectManage); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if s.resolver == nil {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject resolver unavailable")
	}
	resolved, err := s.resolver.ResolveSubject(ctx, workspaceID, request.SubjectType, request.SubjectID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	next := request
	next.Status, next.ResolvedIdentity, next.VerifiedBy, next.SecondFactorRef, next.UpdatedAt = lifecyclemodel.SubjectRequestVerified, resolved, principal.UserID, strings.TrimSpace(secondFactor), time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.verified")
}

func (s *LifecycleApplicationService) PreviewSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, PermissionSubjectManage); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	preview := map[string]json.RawMessage{}
	for _, handler := range s.subjectHandlers {
		payload, handlerErr := handler.PreviewSubject(ctx, workspaceID, request.ResolvedIdentity)
		if handlerErr != nil {
			return lifecyclemodel.SubjectRequest{}, handlerErr
		}
		preview[handler.Owner(ctx)] = payload
	}
	raw, _ := json.Marshal(preview)
	next := request
	next.Status, next.ImpactPreview, next.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, raw, time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.previewed")
}

func (s *LifecycleApplicationService) ApproveSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, PermissionSubjectManage); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	next := request
	next.Status, next.ApprovedBy, next.UpdatedAt = lifecyclemodel.SubjectRequestApproved, principal.UserID, time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.approved")
}

func (s *LifecycleApplicationService) ExecuteSubjectRequest(ctx context.Context, workspaceID, requestID string, principal principalmodel.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionSubjectManage); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	executing := request
	executing.Status, executing.UpdatedAt = lifecyclemodel.SubjectRequestExecuting, time.Now().UTC()
	executing.ExecutionAttempt++
	executing.ExecutionLeaseEnd = executing.UpdatedAt.Add(5 * time.Minute)
	executing.LastError = ""
	if executing, err = s.transitionSubject(ctx, request, executing, principal.UserID, "lifecycle.subject.executing"); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	result := map[string]json.RawMessage{}
	holds, err := s.repository.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, Owner: "", ResourceType: "data_subject", ResourceID: executing.ResolvedIdentity}, executing.UpdatedAt)
	if err == nil && executing.Kind == lifecyclemodel.SubjectRequestErase && len(holds) > 0 {
		err = fmt.Errorf("subject erasure blocked by legal hold")
	}
	if err == nil && executing.Kind == lifecyclemodel.SubjectRequestErase && s.externalErasure != nil {
		external, externalErr := s.externalErasure.RequestExternalErasure(ctx, executing)
		if externalErr != nil {
			err = externalErr
		} else {
			err = s.repository.SaveExternalErasures(ctx, external)
		}
	}
	if err == nil {
		for _, handler := range s.subjectHandlers {
			var payload json.RawMessage
			if executing.Kind == lifecyclemodel.SubjectRequestExport {
				payload, err = handler.ExportSubject(ctx, workspaceID, executing.ResolvedIdentity)
			} else {
				payload, err = handler.EraseSubject(ctx, workspaceID, executing.ResolvedIdentity, holds)
			}
			if err != nil {
				break
			}
			result[handler.Owner(ctx)] = payload
		}
	}
	completed := executing
	completed.UpdatedAt = time.Now().UTC()
	completed.ExecutionLeaseEnd = time.Time{}
	if err != nil {
		completed.Status, completed.LastError = lifecyclemodel.SubjectRequestFailed, err.Error()
		return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.failed")
	}
	raw, _ := json.Marshal(result)
	if completed.Kind == lifecyclemodel.SubjectRequestExport {
		if s.artifacts == nil {
			err = fmt.Errorf("subject artifact store unavailable")
		} else {
			completed.DownloadExpiresAt = completed.UpdatedAt.Add(24 * time.Hour)
			completed.ResultReference, err = s.artifacts.PutSubjectExport(ctx, workspaceID, completed.ID, raw, completed.DownloadExpiresAt)
		}
	} else {
		completed.ResultReference = "erase-evidence:" + completed.ID
		completed.BackupPending = true
		err = s.repository.SaveDeletionRegistration(ctx, lifecyclemodel.DeletionRegistration{RequestID: completed.ID, WorkspaceID: workspaceID, ResolvedIdentity: completed.ResolvedIdentity, BackupPending: true, Evidence: completed.ResultReference, UpdatedAt: completed.UpdatedAt})
	}
	if err != nil {
		completed.Status, completed.LastError = lifecyclemodel.SubjectRequestFailed, err.Error()
		return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.failed")
	}
	completed.Status = lifecyclemodel.SubjectRequestSucceeded
	return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.succeeded")
}
