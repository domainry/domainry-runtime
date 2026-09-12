package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	identity "github.com/domainry/domainry-identity-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// Remote Identity intentionally has no ProjectRoleCatalogPublisher. Its
// deployment administrator provisions roles through the existing versioned
// administration API after Runtime has published its permission definitions.
// Products never receive this administrator's credential or publish roles.
func provisionBusinessServiceRoles(t *testing.T, f *businessWebFixture) {
	t.Helper()
	// Only the trusted test deployment publishes the product-owned catalog.
	// Borrowed product bindings deliberately do not reconcile owner snapshots.
	registry := f.identity.Permissions()
	reader, ok := registry.(identity.PermissionSnapshotReader)
	if !ok {
		t.Fatal("Identity permission snapshot reader unavailable")
	}
	application := identity.ApplicationRef{WorkspaceID: identity.WorkspaceID(f.cfg.IdentityWorkspaceID), ApplicationKey: identity.ApplicationKey(f.cfg.IdentityAudience)}
	const owner = "tools:user_preferences"
	prior, err := reader.CurrentSourceSnapshot(t.Context(), identity.PermissionSourceSnapshotRequest{Application: application, SourceOwner: owner})
	if err != nil {
		t.Fatal(err)
	}
	definitions := []identity.PermissionDefinition{}
	for _, route := range toolsdk.ToolSettingsRoutes() {
		permission := route.Action.Permission
		definitions = append(definitions, identity.PermissionDefinition{PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, OperationKey: permission.OperationKey, Label: permission.Label, Category: permission.Category, SourceKind: route.Action.SourceKind})
	}
	request, err := identity.NewPermissionReconcileRequest(application, owner, prior.SnapshotHash, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Reconcile(t.Context(), request); err != nil {
		t.Fatal("deployment product permissions", err)
	}
	raw, err := os.ReadFile(f.cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, key, expectedHash string, want int) *httptest.ResponseRecorder {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "http://identity.test"+path, bytes.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		r.Header.Set("Idempotency-Key", key)
		if expectedHash != "" {
			r.Header.Set("Expected-Schema-Hash", expectedHash)
		}
		w := httptest.NewRecorder()
		f.identityHandler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("Identity role provisioning %s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		key := role["key"].(string)
		if key != "headquarters_admin" && key != "business_restricted" && key != "business_field_restricted" && key != "business_action_restricted" {
			continue
		}
		for _, route := range toolsdk.ToolSettingsRoutes() {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": route.Action.Permission.Key, "data_scope": "owner"})
		}
		created := call("POST", "/identity/roles", map[string]any{"role": map[string]any{"key": key, "name": role["name"], "permissions": role["permissions"]}, "business_reason": "Provision the isolated F05 business acceptance role"}, "provision-"+key, "", 201)
		fields := []any{}
		for _, value := range role["field_permissions"].([]any) {
			field := value.(map[string]any)
			fields = append(fields, map[string]any{"resource": field["object_key"], "field": field["field_key"], "visible": field["read"], "editable": field["write"]})
		}
		hash := created.Header().Get("X-Resource-Hash")
		if hash == "" {
			t.Fatal("Identity create returned no schema revision")
		}
		call("PUT", "/identity/roles/"+key+"/field-permissions", map[string]any{"field_permissions": fields, "business_reason": "Publish explicit read-field policy for isolated F05 acceptance"}, "fields-"+key, hash, 200)
	}
}
