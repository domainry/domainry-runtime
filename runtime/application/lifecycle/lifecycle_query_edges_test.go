package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type lifecycleSubjectArtifactProbe struct {
	put           func(context.Context, string, string, json.RawMessage, time.Time) (string, error)
	read          func(context.Context, string, string, time.Time) (json.RawMessage, error)
	deleteExports func(context.Context, time.Time) (int, error)
	deleteStaging func(context.Context, time.Time) (int, error)
}

func (p *lifecycleSubjectArtifactProbe) PutSubjectExport(ctx context.Context, workspaceID, requestID string, payload json.RawMessage, expiresAt time.Time) (string, error) {
	if p.put != nil {
		return p.put(ctx, workspaceID, requestID, payload, expiresAt)
	}
	return "", nil
}

func (p *lifecycleSubjectArtifactProbe) ReadSubjectExport(ctx context.Context, workspaceID, reference string, now time.Time) (json.RawMessage, error) {
	if p.read != nil {
		return p.read(ctx, workspaceID, reference, now)
	}
	return nil, nil
}

func (p *lifecycleSubjectArtifactProbe) DeleteExpiredSubjectExports(ctx context.Context, now time.Time) (int, error) {
	if p.deleteExports != nil {
		return p.deleteExports(ctx, now)
	}
	return 0, nil
}

func (p *lifecycleSubjectArtifactProbe) DeleteExpiredUploadStaging(ctx context.Context, now time.Time) (int, error) {
	if p.deleteStaging != nil {
		return p.deleteStaging(ctx, now)
	}
	return 0, nil
}

func TestLifecycleMetricsAndHealthPropagateRepositoryResults(t *testing.T) {
	now := time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC)
	repositoryFailure := errors.New("metrics repository failed")
	repository := &lifecycleRepositoryProbe{
		metrics: func(context.Context, string, time.Time) (lifecyclemodel.Metrics, error) {
			return lifecyclemodel.Metrics{}, repositoryFailure
		},
		globalMetrics: func(_ context.Context, scope principalmodel.SystemScope, received time.Time) (lifecyclemodel.Metrics, error) {
			if received != now || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
				t.Fatalf("scope=%#v now=%v", scope, received)
			}
			return lifecyclemodel.Metrics{Warning: true, EligibleBacklog: 7, OldestEligible: now.Add(-time.Hour), PurgedTotal: 11, FailureTotal: 2, LegalHoldCount: 3}, nil
		},
	}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := service.Metrics(t.Context(), lifecycleAdmin("workspace-a", "admin"), now); !errors.Is(err, repositoryFailure) {
		t.Fatalf("Metrics error = %v", err)
	}
	if _, err := service.HealthForSystem(t.Context(), principalmodel.SystemScope{}, now); err == nil {
		t.Fatal("HealthForSystem accepted an invalid system scope")
	}
	health, err := service.HealthForSystem(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "health test"), now)
	if err != nil {
		t.Fatal(err)
	}
	if health["warning"] != true || health["eligible_backlog"] != int64(7) || health["failure_total"] != int64(2) || health["legal_hold_count"] != int64(3) {
		t.Fatalf("health=%#v", health)
	}
	repository.globalMetrics = func(context.Context, principalmodel.SystemScope, time.Time) (lifecyclemodel.Metrics, error) {
		return lifecyclemodel.Metrics{}, repositoryFailure
	}
	if _, err := service.HealthForSystem(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "health failure"), now); !errors.Is(err, repositoryFailure) {
		t.Fatalf("HealthForSystem error = %v", err)
	}
}

func TestLifecycleArchiveAndExternalQueriesPreserveErrorsAndRedactSecrets(t *testing.T) {
	repositoryFailure := errors.New("query failed")
	auditFailure := errors.New("audit failed")
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	admin := lifecycleAdmin("workspace-a", "admin")

	repository.listArchive = func(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error) {
		return nil, repositoryFailure
	}
	if _, err := service.ListArchiveEntries(t.Context(), "records", 10, admin); !errors.Is(err, repositoryFailure) {
		t.Fatalf("ListArchiveEntries repository error = %v", err)
	}
	repository.listArchive = func(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error) {
		return []lifecyclemodel.ArchiveEntry{{ID: "archive-1"}}, nil
	}
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	entries, err := service.ListArchiveEntries(t.Context(), "records", 10, admin)
	if len(entries) != 1 || !errors.Is(err, auditFailure) {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}

	repository.listExternal = func(context.Context, string, string) ([]lifecyclemodel.ExternalErasure, error) {
		return []lifecyclemodel.ExternalErasure{{ID: "external-1", ProviderRef: "provider-secret", Evidence: "raw-evidence"}}, repositoryFailure
	}
	items, err := service.ListExternalErasures(t.Context(), "request-1", admin)
	if !errors.Is(err, repositoryFailure) || len(items) != 1 || items[0].ProviderRef != "redacted" || items[0].Evidence != "" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestLifecycleReconcileExternalErasureFailureAndSanitization(t *testing.T) {
	now := time.Date(2026, 7, 19, 2, 3, 4, 0, time.UTC)
	repositoryFailure := errors.New("reconcile failed")
	auditFailure := errors.New("audit failed")
	repository := &lifecycleRepositoryProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	admin := lifecycleAdmin("workspace-a", "admin")
	if _, err := service.ReconcileExternalErasure(t.Context(), "external-1", "", admin, now); err == nil {
		t.Fatal("empty reconciliation evidence accepted")
	}
	repository.reconcileExternal = func(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error) {
		return lifecyclemodel.ExternalErasure{}, false, repositoryFailure
	}
	if _, err := service.ReconcileExternalErasure(t.Context(), "external-1", "proof", admin, now); !errors.Is(err, repositoryFailure) {
		t.Fatalf("repository error = %v", err)
	}
	repository.reconcileExternal = func(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error) {
		return lifecyclemodel.ExternalErasure{}, false, nil
	}
	if _, err := service.ReconcileExternalErasure(t.Context(), "missing", "proof", admin, now); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("not-found error = %v", err)
	}
	repository.reconcileExternal = func(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error) {
		return lifecyclemodel.ExternalErasure{ID: "external-1", ProviderRef: "secret", Evidence: "proof"}, true, nil
	}
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.ReconcileExternalErasure(t.Context(), "external-1", "proof", admin, now); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	item, err := service.ReconcileExternalErasure(t.Context(), "external-1", "proof", admin, now)
	if err != nil || item.ProviderRef != "redacted" || item.Evidence != "" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
}

func TestLifecycleDownloadSubjectExportAvailabilityAndFailurePropagation(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 4, 5, 0, time.UTC)
	base := lifecyclemodel.SubjectRequest{ID: "request-1", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestSucceeded, ResultReference: "artifact-1", DownloadExpiresAt: now.Add(time.Hour)}
	repository := &lifecycleRepositoryProbe{}
	artifacts := &lifecycleSubjectArtifactProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, Artifacts: artifacts})
	admin := lifecycleAdmin("workspace-a", "admin")
	repositoryFailure := errors.New("request lookup failed")
	repository.getSubjectRequest = func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return lifecyclemodel.SubjectRequest{}, false, repositoryFailure
	}
	if _, err := service.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now); !errors.Is(err, repositoryFailure) {
		t.Fatalf("lookup error = %v", err)
	}
	repository.getSubjectRequest = func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return lifecyclemodel.SubjectRequest{}, false, nil
	}
	if _, err := service.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("not-found error = %v", err)
	}

	for name, mutate := range map[string]func(*lifecyclemodel.SubjectRequest){
		"erase kind":     func(value *lifecyclemodel.SubjectRequest) { value.Kind = lifecyclemodel.SubjectRequestErase },
		"pending status": func(value *lifecyclemodel.SubjectRequest) { value.Status = lifecyclemodel.SubjectRequestVerified },
		"zero expiry":    func(value *lifecyclemodel.SubjectRequest) { value.DownloadExpiresAt = time.Time{} },
		"expired":        func(value *lifecyclemodel.SubjectRequest) { value.DownloadExpiresAt = now },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			repository.getSubjectRequest = func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
				return request, true, nil
			}
			if _, err := service.DownloadSubjectExport(t.Context(), "workspace-a", request.ID, admin, now); err == nil || !strings.Contains(err.Error(), "unavailable or expired") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	repository.getSubjectRequest = func(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error) {
		return base, true, nil
	}
	withoutArtifacts := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository})
	if _, err := withoutArtifacts.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now); err == nil {
		t.Fatal("download without artifact store succeeded")
	}

	artifactFailure := errors.New("artifact read failed")
	artifacts.read = func(context.Context, string, string, time.Time) (json.RawMessage, error) { return nil, artifactFailure }
	if _, err := service.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now); !errors.Is(err, artifactFailure) {
		t.Fatalf("artifact error = %v", err)
	}
	payload := json.RawMessage(`{"subject":"export"}`)
	artifacts.read = func(_ context.Context, workspaceID, reference string, received time.Time) (json.RawMessage, error) {
		if workspaceID != base.WorkspaceID || reference != base.ResultReference || received != now {
			t.Fatalf("workspace=%q reference=%q now=%v", workspaceID, reference, received)
		}
		return payload, nil
	}
	auditFailure := errors.New("download audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	got, err := service.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now)
	if string(got) != string(payload) || !errors.Is(err, auditFailure) {
		t.Fatalf("payload=%s err=%v", got, err)
	}
	repository.appendAudit = nil
	got, err = service.DownloadSubjectExport(t.Context(), "workspace-a", base.ID, admin, now)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("payload=%s err=%v", got, err)
	}
}
