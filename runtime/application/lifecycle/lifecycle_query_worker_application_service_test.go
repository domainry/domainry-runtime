package lifecycle

import (
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
)

func TestLifecycleQueriesPreviewAndEmptyWorker(t *testing.T) {
	service, _ := newLifecycleApplicationTestService(t)
	now := time.Now().UTC()
	admin := lifecycleAdmin("workspace-a", "admin-a")
	policy := publishTestPolicy(t, service, admin, now)

	policies, err := service.ListPolicies(t.Context(), admin)
	if err != nil || len(policies) != 1 || policies[0].Policy.Key != policy.Policy.Key {
		t.Fatalf("policies=%+v error=%v", policies, err)
	}
	preview, err := service.PreviewCleanup(t.Context(), admin.WorkspaceID, policy.Policy.Key, admin, now)
	if err != nil || preview.Rows != 0 {
		t.Fatalf("preview=%+v error=%v", preview, err)
	}
	metrics, err := service.Metrics(t.Context(), admin, now)
	if err != nil || metrics.EligibleBacklog != 0 {
		t.Fatalf("metrics=%+v error=%v", metrics, err)
	}
	entries, err := service.ListArchiveEntries(t.Context(), "integration_webhook_nonces", 999, admin)
	if err != nil || len(entries) != 0 {
		t.Fatalf("archive entries=%+v error=%v", entries, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "lifecycle query test")
	health, err := service.HealthForSystem(t.Context(), scope, now)
	if err != nil || health["eligible_backlog"] != int64(0) {
		t.Fatalf("health=%+v error=%v", health, err)
	}
	processed, err := service.ProcessRunnableCleanupJobs(t.Context(), "worker-a", 100, 10, now, scope)
	if err != nil || processed != 0 {
		t.Fatalf("processed=%d error=%v", processed, err)
	}

	unauthorized := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: "viewer"}}, accessfixture.Bundle{Key: "viewer"})
	for name, run := range map[string]func() error{
		"policies": func() error { _, err := service.ListPolicies(t.Context(), unauthorized); return err },
		"preview": func() error {
			_, err := service.PreviewCleanup(t.Context(), admin.WorkspaceID, policy.Policy.Key, unauthorized, now)
			return err
		},
		"metrics": func() error { _, err := service.Metrics(t.Context(), unauthorized, now); return err },
		"archive": func() error {
			_, err := service.ListArchiveEntries(t.Context(), "integration_webhook_nonces", 10, unauthorized)
			return err
		},
	} {
		if err := run(); err == nil {
			t.Fatalf("%s accepted unauthorized principal", name)
		}
	}
}

func TestLifecycleExternalErasureQueriesAndReconciliation(t *testing.T) {
	service, store := newLifecycleApplicationTestService(t)
	now := time.Now().UTC()
	admin := lifecycleAdmin("workspace-a", "admin-a")
	repository := lifecyclepersistence.NewLifecycleStore(store)
	item := lifecyclemodel.ExternalErasure{
		ID: "erasure-1", RequestID: "request-1", WorkspaceID: admin.WorkspaceID,
		ConnectorKey: "probe", ProviderRef: "provider-secret-reference", Status: "pending", Evidence: "raw-provider-evidence",
	}
	if err := repository.SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{item}); err != nil {
		t.Fatal(err)
	}
	items, err := service.ListExternalErasures(t.Context(), item.RequestID, admin)
	if err != nil || len(items) != 1 || items[0].ProviderRef != "redacted" || items[0].Evidence != "" {
		t.Fatalf("erasures=%+v error=%v", items, err)
	}
	if _, err := service.ReconcileExternalErasure(t.Context(), item.ID, "", admin, now); err == nil {
		t.Fatal("empty reconciliation evidence accepted")
	}
	if _, err := service.ReconcileExternalErasure(t.Context(), "missing", "ticket-missing", admin, now); err == nil {
		t.Fatal("missing external erasure reconciled")
	}
	reconciled, err := service.ReconcileExternalErasure(t.Context(), item.ID, "ticket-42", admin, now)
	if err != nil || reconciled.Status != "reconciled" || reconciled.ProviderRef != "redacted" || reconciled.Evidence != "" {
		t.Fatalf("reconciled=%+v error=%v", reconciled, err)
	}
	unauthorized := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: "viewer"}}, accessfixture.Bundle{Key: "viewer"})
	if _, err := service.ListExternalErasures(t.Context(), "", unauthorized); err == nil {
		t.Fatal("external erasures visible without permission")
	}
	if _, err := service.ReconcileExternalErasure(t.Context(), item.ID, "ticket", unauthorized, now); err == nil {
		t.Fatal("external erasure reconciled without permission")
	}
}
