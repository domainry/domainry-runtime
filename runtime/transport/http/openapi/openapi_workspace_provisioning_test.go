package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestWorkspaceProvisioningOpenAPIIncludesProjectionReceiptsWithoutFailureControl(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths := spec["paths"].(map[string]any)
	provision, ok := paths["/workspaces"].(map[string]any)
	if !ok || provision["post"] == nil {
		t.Fatal("workspace provisioning operation is missing")
	}
	reconcile, ok := paths["/workspaces/{workspaceID}/role-reconciliations"].(map[string]any)
	if !ok || reconcile["post"] == nil {
		t.Fatal("workspace role reconciliation operation is missing")
	}
	raw, err := json.Marshal(map[string]any{"provision": provision, "reconcile": reconcile})
	if err != nil {
		t.Fatal(err)
	}
	contract := strings.ToLower(string(raw))
	for _, required := range []string{"application_projection_ids", "must_change_password", "workspace_id", "provisioned_roles"} {
		if !strings.Contains(contract, required) {
			t.Fatalf("workspace provisioning OpenAPI omits %q: %s", required, contract)
		}
	}
	for _, forbidden := range []string{"failure_point", "fault_injection", "inject_failure", "x-workspace-provision-failure"} {
		if strings.Contains(contract, forbidden) {
			t.Fatalf("acceptance-only failure control leaked into OpenAPI as %q", forbidden)
		}
	}
}
