package openapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

type openAPIWorkspaceBootstrapParticipant struct {
	descriptor runtimeext.WorkspaceBootstrapDescriptor
}

func (participant openAPIWorkspaceBootstrapParticipant) Descriptor() runtimeext.WorkspaceBootstrapDescriptor {
	return participant.descriptor
}

func (openAPIWorkspaceBootstrapParticipant) BuildWorkspaceBootstrap(context.Context, runtimeext.WorkspaceBootstrapContext, map[string]any) ([]runtimeext.WorkspaceBootstrapRecord, error) {
	return nil, nil
}

func TestWorkspaceProvisioningOpenAPIIsWorkspaceOnlyAndForbidsUnboundApplicationInput(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths := spec["paths"].(map[string]any)
	provision, ok := paths["/workspaces"].(map[string]any)
	if !ok || provision["post"] == nil {
		t.Fatal("workspace provisioning operation is missing")
	}
	if paths["/workspaces/{workspaceID}/role-reconciliations"] != nil {
		t.Fatal("retired role reconciliation operation is still exposed")
	}
	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}
	contract := strings.ToLower(string(raw))
	for _, required := range []string{"commercial_configuration", "first_store_code", "must_change_password", "credential_delivery_status"} {
		if !strings.Contains(contract, required) {
			t.Fatalf("workspace provisioning OpenAPI omits %q: %s", required, contract)
		}
	}
	for _, forbidden := range []string{"application_bootstrap", "tenant", "workspace_id", "provisioned_roles", "failure_point", "fault_injection", "inject_failure", "x-workspace-provision-failure"} {
		if strings.Contains(contract, forbidden) {
			t.Fatalf("acceptance-only failure control leaked into OpenAPI as %q", forbidden)
		}
	}
}

func TestWorkspaceProvisioningOpenAPIProjectsExactBootstrapDescriptor(t *testing.T) {
	minimum, maximum, maxLength := 0.0, 100.0, 3
	descriptor := runtimeext.WorkspaceBootstrapDescriptor{
		Key: "store_settings", InputType: "nightpos.bootstrap.StoreSettings", ParticipantRevision: "nightpos-v3",
		InputFields: []runtimeext.WorkspaceBootstrapInputField{
			{Key: "mode", Type: runtimeext.WorkspaceBootstrapInputString, Required: true, Default: "retail", Enum: []string{"retail", "restaurant"}, MaxLength: &maxLength, Pattern: `^[a-z]+$`},
			{Key: "tax_rate", Type: runtimeext.WorkspaceBootstrapInputExactDecimal, Default: "0.10", Minimum: &minimum, Maximum: &maximum},
		},
		Records: []runtimeext.WorkspaceBootstrapRecordCapability{{Key: "settings", ObjectKey: "store_settings", Fields: []string{"mode", "tax_rate"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	spec := BuildWithWorkspaceBootstrap(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", nil, openAPIWorkspaceBootstrapParticipant{descriptor: descriptor})
	raw, err := json.Marshal(spec["paths"].(map[string]any)["/workspaces"])
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, required := range []string{"application_bootstrap", descriptor.InputContractSHA256, descriptor.ParticipantRevision, `"enum":["retail","restaurant"]`, `"x-domainry-maximum":100`, `"x-domainry-exact-decimal":true`, `"default":"0.10"`, `"pattern":"^[a-z]+$"`, `"additionalProperties":false`} {
		if !strings.Contains(contract, required) {
			t.Fatalf("dynamic bootstrap OpenAPI omits %q: %s", required, contract)
		}
	}
}
