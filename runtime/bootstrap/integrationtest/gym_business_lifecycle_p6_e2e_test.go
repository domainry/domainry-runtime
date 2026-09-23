package integrationtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	runtimecomposition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
)

func TestGymFinancialObjectsRejectDirectMutationThroughGenericLifecyclePolicy(t *testing.T) {
	service, principal := newGymLifecycleP6Environment(t)
	handler := gymLifecycleP6Handler(service, principal)

	ledger := gymLifecycleP6Request(t, handler, http.MethodPost, "/records/gym_financial_entry", "ledger-create", map[string]any{"data": map[string]any{
		"business_key": "payment-1", "amount": "100.00", "entry_kind": "payment",
	}}, http.StatusCreated, "")
	ledgerID := fmt.Sprint(ledger["id"])
	gymLifecycleP6Request(t, handler, http.MethodPatch, "/records/gym_financial_entry/items/"+ledgerID, "ledger-update", map[string]any{"data": map[string]any{"amount": "1.00"}}, http.StatusConflict, "backend.record.lifecycle_append_only")
	gymLifecycleP6Request(t, handler, http.MethodDelete, "/records/gym_financial_entry/items/"+ledgerID, "ledger-delete", nil, http.StatusConflict, "backend.record.lifecycle_append_only")

	settlement := gymLifecycleP6Request(t, handler, http.MethodPost, "/records/gym_commission_lock", "settlement-create", map[string]any{"data": map[string]any{
		"status": "draft", "amount": "25.00", "rule_version": "3",
	}}, http.StatusCreated, "")
	settlementID := fmt.Sprint(settlement["id"])
	gymLifecycleP6Request(t, handler, http.MethodPatch, "/records/gym_commission_lock/items/"+settlementID, "settlement-confirm", map[string]any{"data": map[string]any{"status": "confirmed"}}, http.StatusOK, "")
	gymLifecycleP6Request(t, handler, http.MethodPatch, "/records/gym_commission_lock/items/"+settlementID, "settlement-immutable", map[string]any{"data": map[string]any{"amount": "99.00"}}, http.StatusConflict, "backend.record.lifecycle_state_immutable")
	gymLifecycleP6Request(t, handler, http.MethodDelete, "/records/gym_commission_lock/items/"+settlementID, "settlement-delete", nil, http.StatusConflict, "backend.record.lifecycle_state_immutable")
}

func newGymLifecycleP6Environment(t *testing.T) (*runtimecomposition.RuntimeServices, principalmodel.Principal) {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "gym-lifecycle-p6.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	objects := []definitionmodel.ObjectSchema{
		{Key: "gym_financial_entry", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}, Fields: []definitionmodel.FieldSchema{
			{Key: "business_key", Type: "text", Required: true}, {Key: "entry_kind", Type: "text", Required: true}, {Key: "amount", Type: "currency", Required: true, Config: map[string]any{"precision": 12, "scale": 2}},
		}},
		{Key: "gym_commission_lock", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"confirmed", "adjusted"}}, Fields: []definitionmodel.FieldSchema{
			{Key: "status", Type: "text", Required: true}, {Key: "amount", Type: "currency", Required: true, Config: map[string]any{"precision": 12, "scale": 2}}, {Key: "rule_version", Type: "text", Required: true},
		}},
	}
	for _, object := range objects {
		planToProduceCreateTable(t, store, object)
	}
	role := accessfixture.Bundle{Key: "gym_finance_fixture_operator", Permissions: []string{
		"gym_financial_entry.read", "gym_financial_entry.create", "gym_financial_entry.update", "gym_financial_entry.delete",
		"gym_commission_lock.read", "gym_commission_lock.create", "gym_commission_lock.update", "gym_commission_lock.delete",
	}, DataPolicies: []accessfixture.DataPolicyFixture{
		{ObjectKey: "gym_financial_entry", Scope: "all", Read: true, Write: true}, {ObjectKey: "gym_commission_lock", Scope: "all", Read: true, Write: true},
	}}
	service := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{ProjectKey: "gym-lifecycle-p6-fixture", SchemaVersion: "1", Name: "Gym Lifecycle P6 Fixture", Objects: objects, Integrations: connectormodel.IntegrationSchema{}, Store: store})
	return service, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "finance-operator", WorkspaceID: "workspace-primary"}}, role)
}

func gymLifecycleP6Handler(service *runtimecomposition.RuntimeServices, principal principalmodel.Principal) http.Handler {
	handler := recordhttp.NewRecordsHandler(recordhttp.RecordsDependencies{
		Queries: service.Applications().Records, Actions: service.Applications().Actions, Audit: service.Applications().Audit, Permissions: service.Applications().Schema,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(writer http.ResponseWriter, status int, value any) {
			writer.WriteHeader(status)
			_ = json.NewEncoder(writer).Encode(value)
		},
		WriteServiceError: func(writer http.ResponseWriter, _ *http.Request, err error) {
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
			writer.WriteHeader(status)
			_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"code": apperror.CodeOf(err)}})
		},
		DecodeJSON: func(writer http.ResponseWriter, request *http.Request, target any) bool {
			if err := json.NewDecoder(request.Body).Decode(target); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux
}

func gymLifecycleP6Request(t *testing.T, handler http.Handler, method, path, idempotencyKey string, body any, wantStatus int, wantCode string) map[string]any {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var decoded map[string]any
	if response.Body.Len() > 0 {
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	}
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	if wantCode != "" {
		errorBody, _ := decoded["error"].(map[string]any)
		if errorBody["code"] != wantCode {
			t.Fatalf("%s %s code=%v want=%s body=%s", method, path, errorBody["code"], wantCode, response.Body.String())
		}
	}
	return decoded
}
