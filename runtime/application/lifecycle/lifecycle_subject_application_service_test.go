package lifecycle

import (
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
)

func TestSubjectExportRequiresVerificationIndependentApprovalAndExpiringAuditedDownload(t *testing.T) {
	service, store := newLifecycleApplicationTestService(t)
	requester := lifecycleAdmin("workspace-a", "requester")
	request, err := service.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, SubjectType: "user", SubjectID: "user-1", Reason: "access request"}, requester)
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-evidence", lifecycleAdmin("workspace-a", "verifier"))
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "reviewer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, requester); err == nil {
		t.Fatal("self approval accepted")
	}
	request, err = service.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "approver"))
	if err != nil {
		t.Fatal(err)
	}
	request, err = service.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, lifecycleAdmin("workspace-a", "executor"))
	if err != nil || request.Status != lifecyclemodel.SubjectRequestSucceeded || request.ResultReference == "" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	payload, err := service.DownloadSubjectExport(t.Context(), "workspace-a", request.ID, requester, time.Now().UTC())
	if err != nil || len(payload) == 0 {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
	if _, err := service.DownloadSubjectExport(t.Context(), "workspace-a", request.ID, requester, request.DownloadExpiresAt.Add(time.Second)); err == nil {
		t.Fatal("expired download accepted")
	}
	deleted, err := service.CleanupExpiredSubjectArtifacts(t.Context(), request.DownloadExpiresAt.Add(time.Second), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "expire subject export"))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	stored, found, err := lifecyclepersistence.NewLifecycleStore(store).GetSubjectRequest(t.Context(), "workspace-a", request.ID)
	if err != nil || !found || stored.ResultReference != "" {
		t.Fatalf("stored=%#v found=%v err=%v", stored, found, err)
	}
}
