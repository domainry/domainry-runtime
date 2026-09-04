package operations

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsHTTPExposesReceiptsButNotUnownedCommandSubmission(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-http.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{
		operationscontract.ActionEnableAutomationRule,
		operationscontract.ActionListOperations,
		operationscontract.ActionGetOperation,
		operationscontract.ActionEnableMaintenance,
		operationscontract.ActionListControls,
	}})
	operationsStore := operationspersistence.NewOperationsStore(runtimeStore)
	service := operationsapplication.NewOperationsApplicationService(operationsStore, nil, nil, func() string { return "http-1" })
	controlOperations := operationsapplication.NewOperationsApplicationService(operationsStore, nil, nil, func() string { return "http-control" })
	handler := NewOperationsHandler(OperationsDependencies{
		Service: service, Controls: operationsapplication.NewOperationsControlApplicationService(operationsStore, controlOperations, operationsStore, nil), Principal: func(*http.Request) principalmodel.Principal { return principal },
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
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
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			case apperror.KindConflict:
				status = http.StatusConflict
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": apperror.CodeOf(err)})
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	rejected := httptest.NewRecorder()
	mux.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/operations", nil))
	if rejected.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /operations status=%d, want method not allowed", rejected.Code)
	}
	created, _, err := service.Submit(t.Context(), operationsapplication.OperationsSubmitRequest{
		Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "daily-report", Reason: "release", Payload: map[string]any{"mode": "manual"},
	}, "operation-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	operationID := created.Command.ID
	if operationID != "operation_http-1" {
		t.Fatalf("operation id=%s", operationID)
	}
	if actionKey := created.Command.ActionKey; actionKey != operationscontract.ActionEnableAutomationRule {
		t.Fatalf("server-derived action_key=%v", actionKey)
	}
	receipt := operationsRequest(t, mux, http.MethodGet, "/operations/"+operationID, "", nil, http.StatusOK)
	if receipt["status_url"] != "/operations/"+operationID {
		t.Fatalf("receipt=%#v", receipt)
	}
	listed := operationsRequest(t, mux, http.MethodGet, "/operations?status=created", "", nil, http.StatusOK)
	if listed["count"].(float64) != 1 {
		t.Fatalf("listed=%#v", listed)
	}
	control := operationsRequest(t, mux, http.MethodPut, "/operations/controls/maintenance/runtime", "maintenance-key", map[string]any{"active": true, "reason": "restore drill", "reference": "CHG-42", "expected_revision": 0}, http.StatusOK)
	if control["control"].(map[string]any)["state"] != "active" || control["receipt"].(map[string]any)["command"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("control=%#v", control)
	}
	controls := operationsRequest(t, mux, http.MethodGet, "/operations/controls?kind=maintenance", "", nil, http.StatusOK)
	if controls["count"].(float64) != 1 {
		t.Fatalf("controls=%#v", controls)
	}
}

func operationsRequest(t *testing.T, handler http.Handler, method, path, key string, body any, want int) map[string]any {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &encoded)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
	}
	result := map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
