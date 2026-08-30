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
		{Key: "z", ObjectKey: "z", Kind: "object_create", IdempotencyKeys: []string{"request"}},
		{Key: "ignored"},
		{Key: "a", ObjectKey: "a", Kind: "record_update"},
	}
	contracts := GuardedWriteContracts(actions)
	if len(contracts) != 0 {
		t.Fatalf("contracts=%#v", contracts)
	}
}

func TestMetadataDictionaryLocaleConditionEdges(t *testing.T) {
	items := []appschemamodel.DictionaryItemSchema{{Key: "fr", Locale: "fr"}}
	if got := dictionaryItemsForLocale(items, ""); len(got) != 1 {
		t.Fatalf("empty locale fallback=%#v", got)
	}
	if got := dictionaryItemsForLocale(items, "fr"); len(got) != 1 || got[0].Key != "fr" {
		t.Fatalf("localized-only=%#v", got)
	}
	service := NewApplicationSchemaDictionaryDomainService([]appschemamodel.DictionarySchema{{Key: "status"}})
	if _, _, err := service.Items(t.Context(), dictionaryFailureRepository{}, "status", "", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataVisibilityRemainingCompoundOperands(t *testing.T) {
	role := accessfixture.Bundle{Permissions: []string{"customer.read", "customer.approve"}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role)
	if !actionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "customer", RequiresPermission: "customer.approve"}) {
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
