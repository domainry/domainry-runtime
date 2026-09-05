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

func TestIdentityFixtureKeepsExplicitModuleOwnerPolicyWithoutBroaderSyntheticScope(t *testing.T) {
	binding, err := NewIdentityFactory(IdentityFixtureConfig{
		Roles: []IdentityFixtureRole{{
			Key:         "exporter",
			Permissions: []string{"data_exchange.jobs.get"},
			DataPolicies: []identitysdk.DataPolicy{{
				Key: "owner-job-get", Resource: "data_exchange.jobs", Action: "get", Effect: identitysdk.EffectAllow,
				DataScopes: []identitysdk.DataScope{identitysdk.DataScopeOwner},
				Predicate:  identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"},
			}},
		}},
	}).Open(context.Background(), identitysdk.ApplicationRef{WorkspaceID: "workspace-primary", ApplicationKey: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	bundle := binding.(*manifestIdentityBinding).accessBundle("exporter", "exporter")
	if len(bundle.DataPolicies) != 1 || bundle.DataPolicies[0].DataScopes[0] != identitysdk.DataScopeOwner {
		t.Fatalf("explicit owner policy was broadened by fixture defaults: %+v", bundle.DataPolicies)
	}
}
