package testkit

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestIdentityFixtureSeparatesExactFunctionGrantsFromBusinessDataEnvelope(t *testing.T) {
	binding, err := NewIdentityFactory(IdentityFixtureConfig{
		Roles: []IdentityFixtureRole{
			{Key: "workspace_governor", AllowAllBusinessData: true},
			{Key: "customer_editor", Permissions: []string{"customer.read", "customer.update"}, AllowAllBusinessData: true},
		},
	}).Open(context.Background(), identitysdk.ApplicationRef{WorkspaceID: "workspace-primary", ApplicationKey: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	registry := binding.(*manifestIdentityBinding)
	registry.permissions = map[string]map[string]identitysdk.PermissionDefinition{
		"runtime:object:customer": {
			"customer.read":   {PermissionKey: "customer.read", ResourceKey: "customer", OperationKey: "read", SourceKind: "object_default"},
			"customer.update": {PermissionKey: "customer.update", ResourceKey: "customer", OperationKey: "update", SourceKind: "object_default"},
		},
	}

	governor := registry.accessBundle("governor", "workspace_governor")
	if len(governor.FunctionGrants) != 0 {
		t.Fatalf("workspace governor grants=%+v", governor.FunctionGrants)
	}
	if len(governor.DataPolicies) != 0 || len(governor.FieldPolicies) != 0 {
		t.Fatalf("AllowAllBusinessData must not invent SDK policies: data=%+v fields=%+v", governor.DataPolicies, governor.FieldPolicies)
	}

	editor := registry.accessBundle("editor", "customer_editor")
	if len(editor.FunctionGrants) != 2 {
		t.Fatalf("customer editor grants=%+v", editor.FunctionGrants)
	}
	if len(editor.DataPolicies) != 2 {
		t.Fatalf("customer editor data policies=%+v", editor.DataPolicies)
	}
	if len(editor.FieldPolicies) != 1 {
		t.Fatalf("customer editor field policies=%+v", editor.FieldPolicies)
	}
	fields := editor.FieldPolicies[0]
	if fields.Resource != "customer" || fields.Field != "*" || !fields.Read || !fields.Write || fields.Export {
		t.Fatalf("customer editor field envelope=%+v", fields)
	}
}
