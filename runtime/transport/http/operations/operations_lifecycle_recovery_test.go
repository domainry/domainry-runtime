package operations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	localartifact "github.com/domainry/domainry-runtime/runtime/infrastructure/lifecycleartifact/filesystem"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestLifecycleGovernanceHandlersExecuteThroughHTTP(t *testing.T) {
	mux, repository := newLifecycleGovernanceHTTPMux(t)
	now := time.Now().UTC()
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{
		Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration",
		Class: lifecyclemodel.RetentionClassTechnical, DefaultRetention: time.Hour, MinimumRetention: 15 * time.Minute,
		WorkspaceMayExtend: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete,
	}, Revision: 1}
	published := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/policies", "", policy, http.StatusCreated)
	if published["status"] != "published" || published["published_at"] == "" {
		t.Fatalf("published=%#v", published)
	}
	policies := operationsRequest(t, mux, http.MethodGet, "/operations/lifecycle/policies", "", nil, http.StatusOK)
	if policies["count"].(float64) != 1 {
		t.Fatalf("policies=%#v", policies)
	}
	preview := operationsRequest(t, mux, http.MethodGet, "/operations/lifecycle/cleanup/preview?policy_key=integration.webhook_nonce.v1", "", nil, http.StatusOK)
	if preview["rows"].(float64) != 0 {
		t.Fatalf("preview=%#v", preview)
	}

	job := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/cleanup/jobs", "", lifecyclemodel.CleanupJob{
		PolicyKey: "integration.webhook_nonce.v1", PolicyVersion: "1", Operation: lifecyclemodel.OperationPurge, Reason: "retention cleanup",
	}, http.StatusAccepted)
	jobID := job["id"].(string)
	completed := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/cleanup/jobs/"+jobID+"/run?batch_size=10", "cleanup-run", nil, http.StatusOK)
	if completed["status"] != "succeeded" {
		t.Fatalf("completed=%#v", completed)
	}
	metrics := operationsRequest(t, mux, http.MethodGet, "/operations/lifecycle/metrics", "", nil, http.StatusOK)
	if metrics["purged_total"].(float64) != 0 {
		t.Fatalf("metrics=%#v", metrics)
	}
	archiveRequest := httptest.NewRequest(http.MethodGet, "/operations/lifecycle/archive?source_table=integration_webhook_nonces&limit=5", nil)
	archiveResponse := httptest.NewRecorder()
	mux.ServeHTTP(archiveResponse, archiveRequest)
	var archive []lifecyclemodel.ArchiveEntry
	if archiveResponse.Code != http.StatusOK || json.Unmarshal(archiveResponse.Body.Bytes(), &archive) != nil || len(archive) != 0 {
		t.Fatalf("archive status=%d body=%s", archiveResponse.Code, archiveResponse.Body.String())
	}

	hold := lifecyclemodel.LegalHold{
		Owner: "integration", ResourceType: "integration_webhook_nonces", ResourceID: "nonce-1",
		Reason: "legal case", Authority: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour), AuditEvidence: "case-1",
	}
	createdHold := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/legal-holds", "", hold, http.StatusCreated)
	holdID := createdHold["ID"].(string)
	ended := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/legal-holds/"+holdID+"/end", "", map[string]any{
		"authority": "legal", "evidence": "case closed", "ended_at": now,
	}, http.StatusOK)
	if ended["EndsAt"] == nil {
		t.Fatalf("ended=%#v", ended)
	}

	erasure := lifecyclemodel.ExternalErasure{ID: "erasure-http-1", RequestID: "request-http-1", WorkspaceID: "workspace-a", ConnectorKey: "probe", ProviderRef: "provider-secret", Status: "pending", Evidence: "raw-evidence"}
	if err := repository.SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{erasure}); err != nil {
		t.Fatal(err)
	}
	erasures := operationsRequest(t, mux, http.MethodGet, "/operations/lifecycle/external-erasures?request_id=request-http-1", "", nil, http.StatusOK)
	if erasures["count"].(float64) != 1 {
		t.Fatalf("erasures=%#v", erasures)
	}
	reconciled := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/external-erasures/erasure-http-1/reconcile", "", map[string]any{"evidence": "ticket-42"}, http.StatusOK)
	if reconciled["status"] != "reconciled" || reconciled["provider_ref"] != "redacted" {
		t.Fatalf("reconciled=%#v", reconciled)
	}
	replayed := operationsRequest(t, mux, http.MethodPost, "/operations/lifecycle/deletions/replay?limit=10", "", nil, http.StatusOK)
	if replayed["replayed"].(float64) != 0 {
		t.Fatalf("replayed=%#v", replayed)
	}
}

func TestLifecycleGovernanceHandlersRejectMalformedJSON(t *testing.T) {
	mux, _ := newLifecycleGovernanceHTTPMux(t)
	for _, path := range []string{
		"/operations/lifecycle/policies",
		"/operations/lifecycle/legal-holds",
		"/operations/lifecycle/legal-holds/hold-1/end",
		"/operations/lifecycle/cleanup/jobs",
		"/operations/lifecycle/external-erasures/erasure-1/reconcile",
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{"))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/operations/lifecycle/policies",
		"/operations/lifecycle/cleanup/preview?policy_key=integration.webhook_nonce.v1",
		"/operations/lifecycle/metrics",
		"/operations/lifecycle/archive?source_table=integration_webhook_nonces",
		"/operations/lifecycle/external-erasures",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Deny", "true")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("unauthorized %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func newLifecycleGovernanceHTTPMux(t *testing.T) (*http.ServeMux, lifecyclepersistence.LifecycleStore) {
	t.Helper()
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-governance-http.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := lifecyclepersistence.NewLifecycleStore(runtimeStore)
	executors := lifecyclepersistence.DefaultOwnerExecutors(runtimeStore)
	ports := make([]lifecyclecontract.OwnerLifecycleExecutor, 0, len(executors))
	for index := range executors {
		ports = append(ports, executors[index])
	}
	subject := lifecycleHTTPSubjectPort{}
	lifecycleService := lifecycleapplication.NewLifecycleApplicationService(t.Context(), lifecycleapplication.LifecycleApplicationDependencies{
		Repository: repository, Executors: ports, SubjectResolver: subject,
		SubjectHandlers: []lifecyclecontract.SubjectDataHandler{subject}, Artifacts: localartifact.NewSubjectStore(t.TempDir()),
	})
	operationsRepository := operationspersistence.NewOperationsStore(runtimeStore)
	nextID := 0
	operationsService := operationsapplication.NewOperationsApplicationService(operationsRepository, nil, nil, func() string {
		nextID++
		return fmt.Sprintf("lifecycle-http-%d", nextID)
	})
	handler := NewOperationsHandler(OperationsDependencies{
		Service: operationsService, Lifecycle: lifecycleService, Principal: func(r *http.Request) principalmodel.Principal {
			principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
			if r.Header.Get("X-Deny") == "true" {
				accessfixture.Set(&principal, accessfixture.Bundle{})
			}
			return principal
		},
		Admin: func(next http.HandlerFunc) http.HandlerFunc { return next },
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
			status := http.StatusBadRequest
			if apperror.KindOf(err) == apperror.KindInternal {
				status = http.StatusInternalServerError
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": apperror.CodeOf(err)})
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux, repository
}
