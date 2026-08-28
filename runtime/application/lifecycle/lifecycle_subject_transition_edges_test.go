package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type lifecycleSubjectResolverProbe struct {
	resolve func(context.Context, string, string, string) (string, error)
}

func (p *lifecycleSubjectResolverProbe) ResolveSubject(ctx context.Context, workspaceID, subjectType, subjectID string) (string, error) {
	if p.resolve != nil {
		return p.resolve(ctx, workspaceID, subjectType, subjectID)
	}
	return "resolved-identity", nil
}

func lifecyclePendingSubject(now time.Time) lifecyclemodel.SubjectRequest {
	return lifecyclemodel.SubjectRequest{
		ID: "request-1", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport,
		Status: lifecyclemodel.SubjectRequestPendingVerification, SubjectType: "user", SubjectID: "user-1",
		RequestedBy: "requester", Reason: "subject request", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
}

func lifecycleVerifiedSubject(now time.Time) lifecyclemodel.SubjectRequest {
	request := lifecyclePendingSubject(now)
	request.Status, request.ResolvedIdentity, request.VerifiedBy, request.SecondFactorRef = lifecyclemodel.SubjectRequestVerified, "resolved-identity", "verifier", "mfa-proof"
	request.UpdatedAt = now.Add(-30 * time.Minute)
	return request
}

func lifecyclePreviewedSubject(now time.Time) lifecyclemodel.SubjectRequest {
	request := lifecycleVerifiedSubject(now)
	request.Status, request.ImpactPreview, request.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, json.RawMessage(`{"probe":{"rows":1}}`), now.Add(-20*time.Minute)
	return request
}

func TestLifecycleCreateSubjectRequestValidationPersistenceAndAudit(t *testing.T) {
	admin := lifecycleAdmin("workspace-a", "requester")
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	request := lifecyclePendingSubject(time.Now().UTC())
	request.Status, request.RequestedBy, request.CreatedAt, request.UpdatedAt = "", "", time.Time{}, time.Time{}
	invalidKind := request
	invalidKind.Kind = "unknown"
	if _, err := service.CreateSubjectRequest(t.Context(), invalidKind, admin); err == nil {
		t.Fatal("unsupported subject request kind accepted")
	}
	missingIdentity := request
	missingIdentity.SubjectID = ""
	if _, err := service.CreateSubjectRequest(t.Context(), missingIdentity, admin); err == nil {
		t.Fatal("subject request without identity accepted")
	}
	saveFailure := errors.New("subject request save failed")
	repository.saveSubjectRequest = func(context.Context, lifecyclemodel.SubjectRequest) error { return saveFailure }
	if _, err := service.CreateSubjectRequest(t.Context(), request, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.saveSubjectRequest = nil
	auditFailure := errors.New("subject request audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.CreateSubjectRequest(t.Context(), request, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	created, err := service.CreateSubjectRequest(t.Context(), request, admin)
	if err != nil || created.ID != request.ID || created.Status != lifecyclemodel.SubjectRequestPendingVerification || created.RequestedBy != admin.UserID || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v err=%v", created, err)
	}
}

func TestLifecycleVerifySubjectRequestDependencyAndTransitionFailures(t *testing.T) {
	now := time.Now().UTC()
	pending := lifecyclePendingSubject(now)
	repository := &lifecycleRepositoryProbe{getSubjectRequest: func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return pending, true, nil
	}}
	admin := lifecycleAdmin("workspace-a", "verifier")
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, "mfa-proof", admin); err == nil {
		t.Fatal("verification without resolver succeeded")
	}
	resolverFailure := errors.New("subject resolution failed")
	resolver := &lifecycleSubjectResolverProbe{resolve: func(context.Context, string, string, string) (string, error) { return "", resolverFailure }}
	service.resolver = resolver
	if _, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, "mfa-proof", admin); !errors.Is(err, resolverFailure) {
		t.Fatalf("resolver error = %v", err)
	}
	resolver.resolve = nil
	if _, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, " ", admin); err == nil {
		t.Fatal("verification without second factor accepted")
	}
	saveFailure := errors.New("verified request save failed")
	repository.saveSubjectRequest = func(context.Context, lifecyclemodel.SubjectRequest) error { return saveFailure }
	if _, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, "mfa-proof", admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.saveSubjectRequest = nil
	auditFailure := errors.New("verification audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, "mfa-proof", admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	verified, err := service.VerifySubjectRequest(t.Context(), "workspace-a", pending.ID, " mfa-proof ", admin)
	if err != nil || verified.Status != lifecyclemodel.SubjectRequestVerified || verified.ResolvedIdentity != "resolved-identity" || verified.SecondFactorRef != "mfa-proof" {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
}

func TestLifecyclePreviewSubjectRequestHandlerPersistenceAndAuditFailures(t *testing.T) {
	now := time.Now().UTC()
	verified := lifecycleVerifiedSubject(now)
	repository := &lifecycleRepositoryProbe{getSubjectRequest: func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return verified, true, nil
	}}
	handlerFailure := errors.New("preview handler failed")
	handler := &lifecycleSubjectHandlerProbe{preview: func(context.Context, string, string) (json.RawMessage, error) { return nil, handlerFailure }}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, SubjectHandlers: []lifecyclecontract.SubjectDataHandler{handler}})
	admin := lifecycleAdmin("workspace-a", "reviewer")
	if _, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", verified.ID, admin); !errors.Is(err, handlerFailure) {
		t.Fatalf("handler error = %v", err)
	}
	handler.preview = func(_ context.Context, workspaceID, identity string) (json.RawMessage, error) {
		if workspaceID != verified.WorkspaceID || identity != verified.ResolvedIdentity {
			t.Fatalf("workspace=%q identity=%q", workspaceID, identity)
		}
		return json.RawMessage(`{"rows":1}`), nil
	}
	saveFailure := errors.New("preview save failed")
	repository.saveSubjectRequest = func(context.Context, lifecyclemodel.SubjectRequest) error { return saveFailure }
	if _, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", verified.ID, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.saveSubjectRequest = nil
	auditFailure := errors.New("preview audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", verified.ID, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	previewed, err := service.PreviewSubjectRequest(t.Context(), "workspace-a", verified.ID, admin)
	if err != nil || previewed.Status != lifecyclemodel.SubjectRequestPreviewed || len(previewed.ImpactPreview) == 0 {
		t.Fatalf("previewed=%#v err=%v", previewed, err)
	}
}

func TestLifecycleApproveSubjectRequestEnforcesIndependentApprovalAndPersistence(t *testing.T) {
	now := time.Now().UTC()
	previewed := lifecyclePreviewedSubject(now)
	repository := &lifecycleRepositoryProbe{getSubjectRequest: func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return previewed, true, nil
	}}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", previewed.ID, lifecycleAdmin("workspace-a", previewed.RequestedBy)); err == nil {
		t.Fatal("self approval accepted")
	}
	saveFailure := errors.New("approval save failed")
	repository.saveSubjectRequest = func(context.Context, lifecyclemodel.SubjectRequest) error { return saveFailure }
	admin := lifecycleAdmin("workspace-a", "approver")
	if _, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", previewed.ID, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.saveSubjectRequest = nil
	auditFailure := errors.New("approval audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", previewed.ID, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	approved, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", previewed.ID, admin)
	if err != nil || approved.Status != lifecyclemodel.SubjectRequestApproved || approved.ApprovedBy != admin.UserID {
		t.Fatalf("approved=%#v err=%v", approved, err)
	}
}
