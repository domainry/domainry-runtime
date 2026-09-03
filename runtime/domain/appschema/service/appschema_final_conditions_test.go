package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestApplicationSchemaFacadeDoesNotDeriveGuardedWritesFromActionConfig(t *testing.T) {
	provider := schemaServiceProviderStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}}
	service := NewApplicationSchemaDomainService(provider, nil)
	if got := service.ForPrincipal(t.Context(), principalmodel.Principal{}); len(got.Objects) != 1 {
		t.Fatalf("principal snapshot=%#v", got)
	}
	if got := service.ObjectMap(t.Context()); got["customer"].Key != "customer" {
		t.Fatalf("object map=%#v", got)
	}
	actions := []definitionmodel.ActionSchema{
		{Key: "z", ObjectKey: "z", Kind: "object_create"},
		{Key: "ignored"},
		{Key: "a", ObjectKey: "a", Kind: "record_update"},
	}
	contracts := GuardedWriteContracts(actions)
	if len(contracts) != 0 {
		t.Fatalf("contracts=%#v", contracts)
	}
}

func TestMetadataVisibilityRemainingCompoundOperands(t *testing.T) {
	permissions := []string{"customer.read", "customer.approve"}
	role := accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role)
	if !actionAllowed(principal, definitionmodel.ActionSchema{Key: "customer.approve", ObjectKey: "customer"}) {
		t.Fatal("explicit action permission denied")
	}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects:       []definitionmodel.ObjectSchema{{Key: "customer"}},
		GuardedWrites: []appschemamodel.ApplicationSchemaGuardedWriteContract{{ObjectKey: "hidden", ActionKey: "hidden.action"}},
	}
	if got := SnapshotForPrincipal(snapshot, principal); len(got.GuardedWrites) != 0 {
		t.Fatalf("hidden guarded write=%#v", got.GuardedWrites)
	}
}
