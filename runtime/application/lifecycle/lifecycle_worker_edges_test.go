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
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type lifecycleSubjectHandlerProbe struct {
	preview func(context.Context, string, string) (json.RawMessage, error)
	export  func(context.Context, string, string) (json.RawMessage, error)
	erase   func(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error)
}

func (*lifecycleSubjectHandlerProbe) Owner(context.Context) string { return "probe" }
func (p *lifecycleSubjectHandlerProbe) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	if p.preview != nil {
		return p.preview(ctx, workspaceID, identity)
	}
	return nil, nil
}
func (p *lifecycleSubjectHandlerProbe) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	if p.export != nil {
		return p.export(ctx, workspaceID, identity)
	}
	return nil, nil
}
func (p *lifecycleSubjectHandlerProbe) EraseSubject(ctx context.Context, workspaceID, identity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if p.erase != nil {
		return p.erase(ctx, workspaceID, identity, holds)
	}
	return nil, nil
}

type lifecycleUploadArtifactProbe struct {
	reconcile func(context.Context, time.Time, int) (lifecyclecontract.UploadCleanupResult, error)
}

func (*lifecycleUploadArtifactProbe) RegisterUpload(context.Context, lifecyclecontract.UploadArtifact) error {
	return nil
}
func (p *lifecycleUploadArtifactProbe) ReconcileUploadArtifacts(ctx context.Context, now time.Time, limit int) (lifecyclecontract.UploadCleanupResult, error) {
	if p.reconcile != nil {
		return p.reconcile(ctx, now, limit)
	}
	return lifecyclecontract.UploadCleanupResult{}, nil
}

func TestLifecycleProcessRunnableCleanupJobsPropagatesListAndProcessingFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 4, 5, 6, 0, time.UTC)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "worker test")
	listFailure := errors.New("list jobs failed")
	repository := &lifecycleRepositoryProbe{listRunnableJobs: func(context.Context, principalmodel.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error) {
		return nil, listFailure
	}}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if processed, err := service.ProcessRunnableCleanupJobs(t.Context(), "worker-1", 10, 20, now, scope); processed != 0 || !errors.Is(err, listFailure) {
		t.Fatalf("processed=%d err=%v", processed, err)
	}

	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: "missing-policy"}
	repository.listRunnableJobs = func(_ context.Context, received principalmodel.SystemScope, limit int, receivedNow time.Time) ([]lifecyclemodel.CleanupJob, error) {
		if received.Kind != scope.Kind || limit != 20 || receivedNow != now {
			t.Fatalf("scope=%#v limit=%d now=%v", received, limit, receivedNow)
		}
		return []lifecyclemodel.CleanupJob{job}, nil
	}
	repository.claimCleanupJob = func(_ context.Context, workspaceID, id, owner string, ttl time.Duration, received time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		if workspaceID != job.WorkspaceID || id != job.ID || owner != "worker-1" || ttl != 2*time.Minute || received != now {
			t.Fatalf("claim workspace=%q id=%q owner=%q ttl=%v now=%v", workspaceID, id, owner, ttl, received)
		}
		return job, true, nil
	}
	if processed, err := service.ProcessRunnableCleanupJobs(t.Context(), "worker-1", 10, 20, now, scope); processed != 0 || err == nil || !strings.Contains(err.Error(), "policy not found") {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
}

func TestLifecycleReplayRegisteredDeletionsStopsAtEverySafetyBoundary(t *testing.T) {
	registration := lifecyclemodel.DeletionRegistration{RequestID: "request-1", WorkspaceID: "workspace-a", ResolvedIdentity: "identity-1"}
	repository := &lifecycleRepositoryProbe{}
	handler := &lifecycleSubjectHandlerProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, SubjectHandlers: []lifecyclecontract.SubjectDataHandler{handler}})
	admin := lifecycleAdmin("workspace-a", "restore-operator")
	if _, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-b", 10, admin); err == nil {
		t.Fatal("cross-workspace replay accepted")
	}
	listFailure := errors.New("registry list failed")
	repository.listDeletions = func(context.Context, string, int) ([]lifecyclemodel.DeletionRegistration, error) {
		return nil, listFailure
	}
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 0 || !errors.Is(err, listFailure) {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	repository.listDeletions = func(context.Context, string, int) ([]lifecyclemodel.DeletionRegistration, error) {
		return []lifecyclemodel.DeletionRegistration{registration}, nil
	}
	holdFailure := errors.New("hold lookup failed")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, holdFailure
	}
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 0 || !errors.Is(err, holdFailure) {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return []lifecyclemodel.LegalHold{{ID: "hold-1"}}, nil
	}
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 0 || err == nil || !strings.Contains(err.Error(), "legal hold") {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	repository.activeLegalHolds = func(_ context.Context, target lifecyclemodel.ResourceTarget, _ time.Time) ([]lifecyclemodel.LegalHold, error) {
		if target.WorkspaceID != "workspace-a" || target.ResourceType != "data_subject" || target.ResourceID != registration.ResolvedIdentity {
			t.Fatalf("target=%#v", target)
		}
		return nil, nil
	}
	eraseFailure := errors.New("erase failed")
	handler.erase = func(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
		return nil, eraseFailure
	}
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 0 || !errors.Is(err, eraseFailure) {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	handler.erase = nil
	auditFailure := errors.New("replay audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 1 || !errors.Is(err, auditFailure) {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	repository.appendAudit = nil
	if replayed, err := service.ReplayRegisteredDeletions(t.Context(), "workspace-a", 10, admin); replayed != 1 || err != nil {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
}

func TestLifecycleCleanupExpiredSubjectArtifactsPreservesPartialProgress(t *testing.T) {
	now := time.Date(2026, 7, 19, 5, 6, 7, 0, time.UTC)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "artifact cleanup test")
	repository := &lifecycleRepositoryProbe{}
	artifacts := &lifecycleSubjectArtifactProbe{}
	uploads := &lifecycleUploadArtifactProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, Artifacts: artifacts, UploadArtifacts: uploads})
	if _, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("cleanup accepted invalid scope")
	}
	withoutArtifacts := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if deleted, err := withoutArtifacts.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 0 || err != nil {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	exportFailure := errors.New("export cleanup failed")
	artifacts.deleteExports = func(context.Context, time.Time) (int, error) { return 2, exportFailure }
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 2 || !errors.Is(err, exportFailure) {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	artifacts.deleteExports = func(context.Context, time.Time) (int, error) { return 2, nil }
	stagingFailure := errors.New("staging cleanup failed")
	artifacts.deleteStaging = func(context.Context, time.Time) (int, error) { return 3, stagingFailure }
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 5 || !errors.Is(err, stagingFailure) {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	artifacts.deleteStaging = func(context.Context, time.Time) (int, error) { return 3, nil }
	reconcileFailure := errors.New("upload reconciliation failed")
	uploads.reconcile = func(_ context.Context, received time.Time, limit int) (lifecyclecontract.UploadCleanupResult, error) {
		if received != now || limit != 500 {
			t.Fatalf("now=%v limit=%d", received, limit)
		}
		return lifecyclecontract.UploadCleanupResult{Deleted: 4}, reconcileFailure
	}
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 9 || !errors.Is(err, reconcileFailure) {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	uploads.reconcile = func(context.Context, time.Time, int) (lifecyclecontract.UploadCleanupResult, error) {
		return lifecyclecontract.UploadCleanupResult{Deleted: 4}, nil
	}
	expireFailure := errors.New("reference expiry failed")
	repository.expireSubjectExport = func(context.Context, principalmodel.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error) {
		return nil, expireFailure
	}
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 9 || !errors.Is(err, expireFailure) {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	repository.expireSubjectExport = func(context.Context, principalmodel.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error) {
		return []lifecyclemodel.SubjectRequest{{ID: "request-1", WorkspaceID: "workspace-a"}}, nil
	}
	auditFailure := errors.New("expiry audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 9 || !errors.Is(err, auditFailure) {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	repository.appendAudit = nil
	if deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), now, scope); deleted != 9 || err != nil {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
}
