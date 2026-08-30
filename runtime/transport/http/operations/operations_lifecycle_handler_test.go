package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	lifecyclecore "github.com/domainry/domainry-lifecycle-sdk/application"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	lifecyclehttp "github.com/domainry/domainry-runtime/runtime/transport/http/lifecycle"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

type lifecycleHTTPSubjectPort struct{}

func (lifecycleHTTPSubjectPort) ResolveSubject(_ context.Context, _, _, subjectID string) (string, error) {
	if subjectID != "user-1" {
		return "", fmt.Errorf("subject identity not found")
	}
	return subjectID, nil
}
func (lifecycleHTTPSubjectPort) Owner(context.Context) string { return "identity" }
func (lifecycleHTTPSubjectPort) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"rows":1}`), nil
}
func (lifecycleHTTPSubjectPort) ExportSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"id":"user-1"}`), nil
}
func (lifecycleHTTPSubjectPort) EraseSubject(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return json.RawMessage(`{"anonymized":1}`), nil
}

func TestLifecycleSubjectExportHTTPFlowEnforcesWorkspaceExpiryAndAudit(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-http.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	binding, err := lifecyclesdkfixture.Open(t.Context(), store, "lifecycle-http-test")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := binding.Repository()
	port := lifecycleHTTPSubjectPort{}
	service := lifecycleapplication.NewLifecycleApplicationService(t.Context(), lifecyclecore.LifecycleApplicationDependencies{Repository: repository, SubjectResolver: port, SubjectHandlers: []lifecyclecontract.SubjectDataHandler{port}, Artifacts: artifacts})
	handler := lifecyclehttp.NewLifecycleHandler(lifecyclehttp.LifecycleDependencies{
		Service:       service,
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
		Principal: func(r *http.Request) principalmodel.Principal {
			workspaceID := r.Header.Get("X-Workspace")
			if workspaceID == "" {
				workspaceID = "workspace-a"
			}
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: r.Header.Get("X-User")}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, target any) bool {
			if err := json.NewDecoder(r.Body).Decode(target); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return false
			}
			return true
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadRequest)
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	call := func(method, path, user, workspace string, body any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("X-User", user)
		request.Header.Set("X-Workspace", workspace)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	created := call(http.MethodPost, "/operations/lifecycle/subjects", "requester", "workspace-a", lifecyclemodel.SubjectRequest{Kind: lifecyclemodel.SubjectRequestExport, SubjectType: "user", SubjectID: "user-1", Reason: "access request"})
	var request lifecyclemodel.SubjectRequest
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &request) != nil {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	for _, step := range []struct {
		action, user string
		body         any
	}{{"verify", "verifier", map[string]string{"second_factor_ref": "mfa"}}, {"preview", "reviewer", nil}, {"approve", "approver", nil}, {"execute", "executor", nil}} {
		response := call(http.MethodPost, "/operations/lifecycle/subjects/"+request.ID+"/"+step.action, step.user, "workspace-a", step.body)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", step.action, response.Code, response.Body.String())
		}
	}
	if response := call(http.MethodGet, "/operations/lifecycle/subjects/"+request.ID+"/download", "requester", "workspace-a", nil); response.Code != http.StatusOK {
		t.Fatalf("download status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/operations/lifecycle/subjects/"+request.ID+"/download", "requester", "workspace-b", nil); response.Code == http.StatusOK {
		t.Fatal("cross-workspace download succeeded")
	}
	stored, found, err := repository.GetSubjectRequest(t.Context(), "workspace-a", request.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	stored.DownloadExpiresAt, stored.UpdatedAt = time.Now().UTC().Add(-time.Minute), time.Now().UTC()
	if err := repository.SaveSubjectRequest(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	if response := call(http.MethodGet, "/operations/lifecycle/subjects/"+request.ID+"/download", "requester", "workspace-a", nil); response.Code == http.StatusOK {
		t.Fatal("expired download succeeded")
	}
	var audits int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_audit_evidence WHERE workspace_id = ? AND event = ?", "workspace-a", "lifecycle.subject.export_downloaded").Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	eraseCreated := call(http.MethodPost, "/operations/lifecycle/subjects", "erase-requester", "workspace-a", lifecyclemodel.SubjectRequest{Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "user-1", Reason: "erase request"})
	var eraseRequest lifecyclemodel.SubjectRequest
	if eraseCreated.Code != http.StatusAccepted || json.Unmarshal(eraseCreated.Body.Bytes(), &eraseRequest) != nil {
		t.Fatalf("erase create status=%d body=%s", eraseCreated.Code, eraseCreated.Body.String())
	}
	for _, step := range []struct {
		action, user string
		body         any
	}{{"verify", "erase-verifier", map[string]string{"second_factor_ref": "mfa"}}, {"preview", "erase-reviewer", nil}, {"approve", "erase-approver", nil}, {"execute", "erase-executor", nil}} {
		response := call(http.MethodPost, "/operations/lifecycle/subjects/"+eraseRequest.ID+"/"+step.action, step.user, "workspace-a", step.body)
		if response.Code != http.StatusOK {
			t.Fatalf("erase %s status=%d body=%s", step.action, response.Code, response.Body.String())
		}
	}
	registrations, err := repository.ListPendingDeletionRegistrations(t.Context(), "workspace-a", 10)
	if err != nil || len(registrations) != 1 || registrations[0].RequestID != eraseRequest.ID {
		t.Fatalf("registrations=%#v err=%v", registrations, err)
	}
	unknownCreated := call(http.MethodPost, "/operations/lifecycle/subjects", "requester", "workspace-a", lifecyclemodel.SubjectRequest{Kind: lifecyclemodel.SubjectRequestExport, SubjectType: "user", SubjectID: "unknown", Reason: "invalid match"})
	var unknown lifecyclemodel.SubjectRequest
	_ = json.Unmarshal(unknownCreated.Body.Bytes(), &unknown)
	if response := call(http.MethodPost, "/operations/lifecycle/subjects/"+unknown.ID+"/verify", "verifier", "workspace-a", map[string]string{"second_factor_ref": "mfa"}); response.Code == http.StatusOK {
		t.Fatal("unknown subject identity was verified")
	}
}
