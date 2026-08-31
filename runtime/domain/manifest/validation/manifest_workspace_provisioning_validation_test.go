package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestWorkspaceProvisioningProjectionValidation(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects:               []definitionmodel.ObjectSchema{{Key: "tenant_profile", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "workspace_code", Type: "text"}}}},
		WorkspaceProvisioning: []manifestmodel.WorkspaceProvisionProjection{{Key: "tenant_profile", ObjectKey: "tenant_profile", Scope: "headquarters", Data: map[string]any{"name": "$provision.tenant_name", "workspace_code": "$provision.canonical_code"}}},
	}
	state := newValidationState(manifest, nil)
	state.validateObjects()
	state.validateWorkspaceProvisioning()
	if len(state.errs) != 0 {
		t.Fatalf("valid workspace projection rejected: %v", state.errs)
	}

	manifest.WorkspaceProvisioning = append(manifest.WorkspaceProvisioning, manifestmodel.WorkspaceProvisionProjection{Key: "tenant_profile", ObjectKey: "tenant_profile", Scope: "invalid", Data: map[string]any{"workspace_code": "$provision.unknown"}})
	state = newValidationState(manifest, nil)
	state.validateObjects()
	state.validateWorkspaceProvisioning()
	message := state.errs.Error()
	for _, expected := range []string{"must be unique", "must be headquarters or provisioned_workspace", "unsupported provisioning expression", "does not bind required field"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("validation %q missing from %v", expected, state.errs)
		}
	}
}

func TestWorkspaceProvisionedRoleMustBeTenantLoginCompatible(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Roles: []manifestmodel.RoleSchema{{Key: "service", Name: "Service", Audience: "service", AssignmentMode: "system_managed", ProvisionToWorkspaces: true}}}
	state := newValidationState(manifest, nil)
	state.validateRoles()
	message := state.errs.Error()
	if !strings.Contains(message, "tenant login roles") || !strings.Contains(message, "system-managed roles cannot") {
		t.Fatalf("workspace role validation missing: %v", state.errs)
	}
}
