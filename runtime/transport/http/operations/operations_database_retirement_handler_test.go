package operations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestDatabaseRetirementHTTPRejectsEarlyExecutionAndNeverAcceptsSQL(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "retirement-http.db"), IntegrationSecretKey: "retirement-http-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE old_http_table (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	repository := operationspersistence.NewOperationsStore(store)
	service := operationsapplication.NewDatabaseRetirementApplicationService(repository, operationspersistence.NewDatabaseRetirementSQLExecutor(store, nil, nil), nil, func() string { return "http-fixed" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "operations"}}, accessfixture.Bundle{Permissions: []string{"runtime.database.retirement.manage"}})
	audits := 0
	handler := NewOperationsHandler(OperationsDependencies{
		DatabaseRetirement: service,
		Principal:          func(*http.Request) principalmodel.Principal { return principal },
		Admin:              func(next http.HandlerFunc) http.HandlerFunc { return next },
		SecurityAudit: func(_ *http.Request, _ principalmodel.Principal, event, _ string, _ map[string]any) {
			if event == "database_retirement_preview" {
				audits++
			}
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
			status := http.StatusInternalServerError
			if apperror.KindOf(err) == apperror.KindConflict {
				status = http.StatusConflict
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": apperror.CodeOf(err)})
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	discovered := operationsRequest(t, mux, http.MethodPost, "/operations/database-retirements", "", map[string]any{
		"object": map[string]any{"engine": "sqlite", "database": "runtime", "kind": "table", "name": "old_http_table"},
		"owner":  "record",
		"sql":    "DROP TABLE old_http_table",
	}, http.StatusCreated)
	id := discovered["id"].(string)
	listed := operationsRequest(t, mux, http.MethodGet, "/operations/database-retirements?state=discovered&limit=5", "", nil, http.StatusOK)
	if listed["count"].(float64) != 1 {
		t.Fatalf("listed=%#v", listed)
	}
	status := operationsRequest(t, mux, http.MethodGet, "/operations/database-retirements/"+id, "", nil, http.StatusOK)
	if status["retirement"].(map[string]any)["state"] != "discovered" {
		t.Fatalf("status=%#v", status)
	}
	operationsRequest(t, mux, http.MethodPost, "/operations/database-retirements/"+id+"/preview", "", nil, http.StatusConflict)
	handler.auditDatabaseRetirement(httptest.NewRequest(http.MethodPost, "/operations/database-retirements/"+id+"/preview", nil), "preview", id)
	if audits != 1 {
		t.Fatalf("audits=%d", audits)
	}
	operationsRequest(t, mux, http.MethodPost, "/operations/database-retirements/"+id+"/advance", "", map[string]any{"state": "dropped"}, http.StatusConflict)
	operationsRequest(t, mux, http.MethodPost, "/operations/database-retirements/"+id+"/execute", "", map[string]any{"sql": "DROP TABLE old_http_table"}, http.StatusConflict)
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='old_http_table'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("arbitrary SQL reached database: count=%d err=%v", count, err)
	}
}
