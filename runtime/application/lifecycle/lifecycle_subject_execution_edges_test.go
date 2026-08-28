package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type lifecycleExternalErasureProbe struct {
	request func(context.Context, lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error)
}

func (p *lifecycleExternalErasureProbe) RequestExternalErasure(ctx context.Context, request lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error) {
	if p.request != nil {
		return p.request(ctx, request)
	}
	return nil, nil
}

func lifecycleApprovedSubject(now time.Time, kind lifecyclemodel.SubjectRequestKind) lifecyclemodel.SubjectRequest {
	request := lifecyclePreviewedSubject(now)
	request.Kind, request.Status, request.ApprovedBy, request.UpdatedAt = kind, lifecyclemodel.SubjectRequestApproved, "approver", now.Add(-10*time.Minute)
	return request
}

func newLifecycleSubjectExecutionProbe(t *testing.T, request lifecyclemodel.SubjectRequest) (*LifecycleApplicationService, *lifecycleRepositoryProbe, *lifecycleSubjectHandlerProbe) {
	t.Helper()
	repository := &lifecycleRepositoryProbe{getSubjectRequest: func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return request, true, nil
	}}
	handler := &lifecycleSubjectHandlerProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, SubjectHandlers: []lifecyclecontract.SubjectDataHandler{handler}})
	return service, repository, handler
}

func TestLifecycleExecuteSubjectRequestInitialTransitionAndHoldFailures(t *testing.T) {
	request := lifecycleApprovedSubject(time.Now().UTC(), lifecyclemodel.SubjectRequestErase)
	service, repository, _ := newLifecycleSubjectExecutionProbe(t, request)
	admin := lifecycleAdmin("workspace-a", "executor")
	saveFailure := errors.New("execution transition save failed")
	repository.saveSubjectRequest = func(context.Context, lifecyclemodel.SubjectRequest) error { return saveFailure }
	if _, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("transition error = %v", err)
	}

	saved := make([]lifecyclemodel.SubjectRequest, 0, 2)
	repository.saveSubjectRequest = func(_ context.Context, value lifecyclemodel.SubjectRequest) error {
		saved = append(saved, value)
		return nil
	}
	holdFailure := errors.New("legal hold lookup failed")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, holdFailure
	}
	failed, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, holdFailure.Error()) || len(saved) != 2 || saved[1].Status != lifecyclemodel.SubjectRequestFailed {
		t.Fatalf("failed=%#v saved=%#v err=%v", failed, saved, err)
	}
}

func TestLifecycleExecuteSubjectErasureExternalAndHandlerFailures(t *testing.T) {
	request := lifecycleApprovedSubject(time.Now().UTC(), lifecyclemodel.SubjectRequestErase)
	service, repository, handler := newLifecycleSubjectExecutionProbe(t, request)
	admin := lifecycleAdmin("workspace-a", "executor")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, nil
	}
	externalFailure := errors.New("external erasure failed")
	external := &lifecycleExternalErasureProbe{request: func(context.Context, lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error) {
		return nil, externalFailure
	}}
	service.externalErasure = external
	failed, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, externalFailure.Error()) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	external.request = func(context.Context, lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error) {
		return []lifecyclemodel.ExternalErasure{{ID: "external-1"}}, nil
	}
	saveExternalFailure := errors.New("external erasure save failed")
	repository.saveExternal = func(context.Context, []lifecyclemodel.ExternalErasure) error { return saveExternalFailure }
	failed, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, saveExternalFailure.Error()) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	repository.saveExternal = nil
	handlerFailure := errors.New("subject erase failed")
	handler.erase = func(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
		return nil, handlerFailure
	}
	failed, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, handlerFailure.Error()) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
}

func TestLifecycleExecuteSubjectExportConvergesArtifactFailuresToFailedState(t *testing.T) {
	request := lifecycleApprovedSubject(time.Now().UTC(), lifecyclemodel.SubjectRequestExport)
	service, repository, handler := newLifecycleSubjectExecutionProbe(t, request)
	admin := lifecycleAdmin("workspace-a", "executor")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, nil
	}
	handler.export = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"subject":1}`), nil
	}
	saved := make([]lifecyclemodel.SubjectRequest, 0, 2)
	repository.saveSubjectRequest = func(_ context.Context, value lifecyclemodel.SubjectRequest) error {
		saved = append(saved, value)
		return nil
	}
	failed, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, "artifact store unavailable") || len(saved) != 2 || saved[1].Status != lifecyclemodel.SubjectRequestFailed {
		t.Fatalf("failed=%#v saved=%#v err=%v", failed, saved, err)
	}

	artifactFailure := errors.New("artifact write failed")
	artifacts := &lifecycleSubjectArtifactProbe{put: func(context.Context, string, string, json.RawMessage, time.Time) (string, error) {
		return "", artifactFailure
	}}
	service.artifacts = artifacts
	saved = saved[:0]
	failed, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, artifactFailure.Error()) || len(saved) != 2 {
		t.Fatalf("failed=%#v saved=%#v err=%v", failed, saved, err)
	}
	artifacts.put = func(_ context.Context, workspaceID, requestID string, payload json.RawMessage, expiresAt time.Time) (string, error) {
		if workspaceID != request.WorkspaceID || requestID != request.ID || len(payload) == 0 || expiresAt.IsZero() {
			t.Fatalf("workspace=%q request=%q payload=%s expires=%v", workspaceID, requestID, payload, expiresAt)
		}
		return "artifact-1", nil
	}
	saved = saved[:0]
	succeeded, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || succeeded.Status != lifecyclemodel.SubjectRequestSucceeded || succeeded.ResultReference != "artifact-1" || succeeded.DownloadExpiresAt.IsZero() || len(saved) != 2 {
		t.Fatalf("succeeded=%#v saved=%#v err=%v", succeeded, saved, err)
	}
}

func TestLifecycleExecuteSubjectErasureRegistersDeletionOrFailsClosed(t *testing.T) {
	request := lifecycleApprovedSubject(time.Now().UTC(), lifecyclemodel.SubjectRequestErase)
	service, repository, handler := newLifecycleSubjectExecutionProbe(t, request)
	admin := lifecycleAdmin("workspace-a", "executor")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, nil
	}
	handler.erase = func(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
		return json.RawMessage(`{"erased":1}`), nil
	}
	registrationFailure := errors.New("deletion registration failed")
	repository.saveDeletion = func(context.Context, lifecyclemodel.DeletionRegistration) error { return registrationFailure }
	failed, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || failed.Status != lifecyclemodel.SubjectRequestFailed || !strings.Contains(failed.LastError, registrationFailure.Error()) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	repository.saveDeletion = func(_ context.Context, registration lifecyclemodel.DeletionRegistration) error {
		if registration.RequestID != request.ID || registration.ResolvedIdentity != request.ResolvedIdentity || !registration.BackupPending || registration.Evidence == "" {
			t.Fatalf("registration=%#v", registration)
		}
		return nil
	}
	succeeded, err := service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, admin)
	if err != nil || succeeded.Status != lifecyclemodel.SubjectRequestSucceeded || !succeeded.BackupPending || !strings.HasPrefix(succeeded.ResultReference, "erase-evidence:") {
		t.Fatalf("succeeded=%#v err=%v", succeeded, err)
	}
}
