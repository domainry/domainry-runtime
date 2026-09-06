package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

// This regression deliberately uses the real in-process Identity module. The
// ordinary Runtime fixture injects prebuilt AccessBundles and therefore cannot
// catch a broken Runtime -> Identity project catalog handoff.
func TestManagedIdentityCompositionProjectsApplicationFieldPermissionsEverywhere(t *testing.T) {
	dir := t.TempDir()
	manifestPath := managedIdentityFieldAccessManifest(t, dir)
	cfg := initializedIntegrationRuntimeConfig(config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"),
		ManifestPath: manifestPath, UploadDir: filepath.Join(dir, "uploads"), IdentityAudience: "domainry-runtime",
	})

	binding, err := identitymodule.NewFactory(identitymodule.Options{
		IdentityVersion: "managed-composition-test", DatabaseDriver: "sqlite", DatabasePath: filepath.Join(dir, "identity.db"),
	}).Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(t.Context()) })

	runtime := bootstrap.NewWithScheduler(t.Context(), cfg, binding,
		notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()),
		schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()),
		dataexchangefixture.NewFactory(), integrationmodule.NewFactory(),
	)
	t.Cleanup(func() { _ = runtime.CloseContext(t.Context()) })

	session, err := binding.Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{
		WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
		Login: "admin@example.com", Password: "Domainry@2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err = binding.Credentials().ChangePassword(t.Context(), identitysdk.ChangePasswordRequest{
		AccessToken: session.AccessToken, CurrentPassword: "Domainry@2026", NewPassword: "ManagedAccess@2026", IdempotencyKey: "managed-access-password-change",
	})
	if err != nil {
		t.Fatal(err)
	}
	managedIdentityAssignHeadquartersRole(t, binding, session.AccessToken)
	session, err = binding.Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{
		WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
		Login: "admin@example.com", Password: "ManagedAccess@2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := binding.Authorization().ResolveAccess(t.Context(), identitysdk.AccessBundleRequest{Identity: identitysdk.RequestIdentity{AccessToken: session.AccessToken}})
	if err != nil {
		t.Fatal(err)
	}
	assertManagedIdentityCustomerFields(t, bundle.FieldPolicies)
	assertManagedIdentityCustomerOwnerScopes(t, bundle.DataPolicies)

	handler := runtime.Routes()
	list := managedIdentityRequest(t, handler, session.AccessToken, http.MethodGet, "/records/customer?page=1&page_size=10", nil, "", http.StatusOK)
	var page struct {
		Items []struct {
			ID   string         `json:"id"`
			Data map[string]any `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID == "" || page.Items[0].Data["name"] != "Acme" {
		t.Fatalf("record list projection lost readable application fields: %#v", page.Items)
	}
	assertManagedIdentityNoOwner(t, page.Items[0].Data)

	detail := managedIdentityRequest(t, handler, session.AccessToken, http.MethodGet, "/records/customer/items/"+page.Items[0].ID, nil, "", http.StatusOK)
	var detailRecord struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailRecord); err != nil {
		t.Fatal(err)
	}
	if detailRecord.Data["name"] != "Acme" {
		t.Fatalf("record detail projection lost readable field: %#v", detailRecord.Data)
	}
	assertManagedIdentityNoOwner(t, detailRecord.Data)

	action := managedIdentityRequest(t, handler, session.AccessToken, http.MethodPost, "/records/customer/items/"+page.Items[0].ID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Renamed"}}, "managed-customer-rename", http.StatusOK)
	var actionResult struct {
		Record struct {
			Data map[string]any `json:"data"`
		} `json:"record"`
	}
	if err := json.Unmarshal(action.Body.Bytes(), &actionResult); err != nil {
		t.Fatal(err)
	}
	if actionResult.Record.Data["name"] != "Renamed" {
		t.Fatalf("Action response projection lost readable field: %s", action.Body.String())
	}
	assertManagedIdentityNoOwner(t, actionResult.Record.Data)

	exported := managedIdentityRequest(t, handler, session.AccessToken, http.MethodPost, "/records/customer/export?fields=name", nil, "managed-customer-export", http.StatusOK)
	if csv := exported.Body.String(); !strings.Contains(csv, "name") || !strings.Contains(csv, "Renamed") || strings.Contains(csv, "owner") {
		t.Fatalf("authorized CSV projection=%q", csv)
	}
	deniedExport := managedIdentityRequest(t, handler, session.AccessToken, http.MethodPost, "/records/customer/export?fields=owner", nil, "managed-owner-export", http.StatusForbidden)
	if !strings.Contains(deniedExport.Body.String(), "backend.export.no_fields") {
		t.Fatalf("denied CSV error=%s", deniedExport.Body.String())
	}

	allowedReport := managedIdentityRequest(t, handler, session.AccessToken, http.MethodGet, "/report/customer_names/summary?page_size=10", nil, "", http.StatusOK)
	if !strings.Contains(allowedReport.Body.String(), "Renamed") || strings.Contains(allowedReport.Body.String(), "Other") {
		t.Fatalf("authorized Object SQL projection or owner scope=%s", allowedReport.Body.String())
	}
	deniedReport := managedIdentityRequest(t, handler, session.AccessToken, http.MethodGet, "/report/customer_owners/summary?page_size=10", nil, "", http.StatusForbidden)
	if !strings.Contains(deniedReport.Body.String(), "backend.report.object_sql_field_denied") {
		t.Fatalf("denied Object SQL error=%s", deniedReport.Body.String())
	}
}

func managedIdentityFieldAccessManifest(t *testing.T, dir string) string {
	t.Helper()
	source := filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, value := range manifest["objects"].([]any) {
		object := value.(map[string]any)
		if object["key"] == "customer" {
			object["config"] = map[string]any{"field_access_mode": "default_deny"}
		}
	}
	var headquartersRole map[string]any
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] == "admin" {
			role["key"] = "headquarters_admin"
			role["name"] = "Headquarters administrator"
			for _, permissionValue := range role["permissions"].([]any) {
				permission := permissionValue.(map[string]any)
				switch permission["permission_key"] {
				case "customer.read", "customer.update", "customer.export":
					permission["data_scope"] = "owner"
				}
			}
			role["permissions"] = append(role["permissions"].([]any),
				map[string]any{"permission_key": "customer.rename", "data_scope": "owner"},
				map[string]any{"permission_key": "report.summary.get", "data_scope": "all"},
			)
			role["field_permissions"] = []any{
				map[string]any{"object_key": "customer", "field_key": "name", "read": true, "write": true, "export": true},
				map[string]any{"object_key": "customer", "field_key": "owner", "read": false, "write": false, "export": false},
			}
			delete(role, "export_rules")
			headquartersRole = role
		}
	}
	if headquartersRole == nil {
		t.Fatal("source manifest has no administrator role to adapt")
	}
	manifest["roles"] = []any{
		map[string]any{"key": "tenant_admin", "name": "Platform administrator", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
		headquartersRole,
		map[string]any{"key": "store_manager", "name": "Store manager", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
		map[string]any{"key": "staff", "name": "Staff", "permissions": []any{}, "audience": "user", "assignment_mode": "manual"},
	}
	for _, value := range manifest["seed_records"].([]any) {
		seed := value.(map[string]any)
		if seed["object_key"] == "customer" {
			seed["owner_user_id"] = "admin"
		}
	}
	manifest["seed_records"] = append(manifest["seed_records"].([]any), map[string]any{
		"object_key": "customer", "owner_user_id": "restricted_user",
		"data": map[string]any{"__seed_key": "customer_other", "name": "Other", "owner": "restricted_user"},
	})
	manifest["actions"] = []any{map[string]any{
		"key": "customer.rename", "object_key": "customer", "label": "Rename customer", "kind": "record_update", "preconditions": []any{}, "audit_event": "customer_renamed",
	}}
	manifest["reports"] = []any{
		managedIdentityFieldReport("customer_names", "name"),
		managedIdentityFieldReport("customer_owners", "owner"),
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "managed-identity-field-access.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func managedIdentityAssignHeadquartersRole(t *testing.T, binding identitysdk.Binding, accessToken string) {
	t.Helper()
	provider, ok := binding.(identityhttpapi.Provider)
	if !ok {
		t.Fatal("Identity module exposes no HTTP adapter provider")
	}
	for _, adapter := range provider.HTTPAdapters() {
		for _, route := range adapter.Routes() {
			if route.Pattern() != "PUT /identity/users/{userID}/account-and-roles" {
				continue
			}
			body := bytes.NewBufferString(`{"user":{"name":"Admin","email":"admin@example.com","status":"active"},"assignments":[{"role_id":"headquarters_admin"}]}`)
			request := httptest.NewRequest(http.MethodPut, "/identity/users/admin/account-and-roles", body)
			request.Header.Set("Authorization", "Bearer "+accessToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			adapter.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("assign headquarters role status=%d body=%s", response.Code, response.Body.String())
			}
			return
		}
	}
	t.Fatal("Identity module exposes no account-and-roles route")
}

func managedIdentityFieldReport(key, field string) map[string]any {
	return map[string]any{
		"key": key, "name": key,
		"object_sql_v1": map[string]any{
			"sql":            "SELECT c." + field + " AS " + field + " FROM customer AS c LIMIT 10",
			"source_objects": []any{"customer"},
			"parameters":     []any{},
			"result_schema":  []any{map[string]any{"key": field, "kind": "dimension", "type": "text"}},
		},
		"required_permissions": []any{"customer.read"},
	}
}

func managedIdentityRequest(t *testing.T, handler http.Handler, token, method, path string, body any, idempotencyKey string, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	return response
}

func assertManagedIdentityCustomerFields(t *testing.T, policies []identitysdk.FieldPolicy) {
	t.Helper()
	foundName, foundOwner := false, false
	for _, policy := range policies {
		if policy.Resource != "customer" {
			continue
		}
		switch policy.Field {
		case "name":
			foundName = policy.Read && policy.Write && policy.Export
		case "owner":
			foundOwner = !policy.Read && !policy.Write && !policy.Export
		}
	}
	if !foundName || !foundOwner {
		t.Fatalf("effective field policies do not contain the Runtime application object: %#v", policies)
	}
}

func assertManagedIdentityCustomerOwnerScopes(t *testing.T, policies []identitysdk.DataPolicy) {
	t.Helper()
	want := map[identitysdk.Action]bool{"read": false, "update": false, "export": false, "rename": false}
	for _, policy := range policies {
		if policy.Resource != "customer" || policy.Effect != identitysdk.EffectAllow {
			continue
		}
		if _, tracked := want[policy.Action]; !tracked {
			continue
		}
		want[policy.Action] = len(policy.DataScopes) == 1 && policy.DataScopes[0] == identitysdk.DataScopeOwner
	}
	for action, preserved := range want {
		if !preserved {
			t.Fatalf("customer.%s owner scope was not preserved: %#v", action, policies)
		}
	}
}

func assertManagedIdentityNoOwner(t *testing.T, data map[string]any) {
	t.Helper()
	if _, leaked := data["owner"]; leaked {
		t.Fatalf("record projection exposed denied field: %#v", data)
	}
}
