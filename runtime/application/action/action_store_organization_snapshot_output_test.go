package action

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestProjectStoreOrganizationSnapshotOutputIsStrictAndReappliesFieldSecurity(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "store_config", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Required: true},
		{Key: "secret", Type: "text", Config: map[string]any{"sensitive": true}},
	}}
	action := definitionmodel.ActionSchema{Key: "store_config.snapshot", Kind: definitionmodel.ActionKindObjectOperation, OutputFields: []definitionmodel.ActionOutputField{{
		Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config"},
	}}}
	input := map[string]any{"stores": map[string]any{"items": []any{map[string]any{
		"organization": map[string]any{"reference": "org-1", "code": "north", "name": "North", "status": "active", "sort_order": float64(1), "version": float64(2)},
		"store_config": map[string]any{"record_id": "config-1", "revision": "2026-09-06T00:00:00Z", "data": map[string]any{"name": "North config", "secret": "top-secret"}},
	}}}}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "store_config", FieldKey: "name", Read: true},
		{ObjectKey: "store_config", FieldKey: "secret", Read: true, Masked: true},
	}})
	projected, err := ProjectBusinessHandlerOutput(t.Context(), principal, action, input, func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key })
	if err != nil {
		t.Fatal(err)
	}
	page := projected["stores"].(map[string]any)
	item := page["items"].([]any)[0].(map[string]any)
	data := item["store_config"].(map[string]any)["data"].(map[string]any)
	if data["name"] != "North config" || data["secret"] != "****cret" {
		t.Fatalf("projected data=%#v", data)
	}
	if !reflect.DeepEqual(item["organization"], input["stores"].(map[string]any)["items"].([]any)[0].(map[string]any)["organization"]) {
		t.Fatalf("organization changed: %#v", item["organization"])
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBusinessHandlerOutput(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectBusinessHandlerOutput(t.Context(), principal, action, decoded, func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key }); err != nil {
		t.Fatalf("snapshot decoded with UseNumber was rejected: %v", err)
	}

	broken := map[string]any{"stores": map[string]any{"items": []any{map[string]any{
		"organization": map[string]any{"reference": "org-1", "code": "north", "name": "North", "status": "active", "sort_order": float64(1), "version": float64(2)},
		"store_config": map[string]any{"record_id": "config-1", "revision": "revision-1", "data": map[string]any{"secret": "top-secret"}},
	}}}}
	if _, err := ProjectBusinessHandlerOutput(t.Context(), principal, action, broken, func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key }); err == nil {
		t.Fatal("snapshot missing required singleton data field was accepted")
	}
	overlongCursor := map[string]any{"stores": map[string]any{"items": []any{}, "next_cursor": string(make([]byte, 2049))}}
	if _, err := ProjectBusinessHandlerOutput(t.Context(), principal, action, overlongCursor, func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key }); err == nil {
		t.Fatal("snapshot with an overlong opaque cursor was accepted")
	}
}

func TestPublishedStoreOrganizationSnapshotRequiresCatalogAndListGrants(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "store_config.snapshot", Kind: definitionmodel.ActionKindObjectOperation, OutputFields: []definitionmodel.ActionOutputField{{
		Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config"},
	}}}
	descriptor := runtimeext.HandlerDescriptor{
		StoreOrganizationCatalog: &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25},
		ObjectCapabilities:       []runtimeext.ActionObjectCapability{{ObjectKey: "store_config", Operations: []string{"list"}}},
	}
	if err := validateStoreOrganizationSnapshotOutput(action, descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.StoreOrganizationCatalog = nil
	if err := validateStoreOrganizationSnapshotOutput(action, descriptor); err == nil {
		t.Fatal("snapshot without catalog was accepted")
	}
	descriptor.StoreOrganizationCatalog = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
	descriptor.ObjectCapabilities[0].Operations = []string{"get"}
	if err := validateStoreOrganizationSnapshotOutput(action, descriptor); err == nil {
		t.Fatal("snapshot without list grant was accepted")
	}
	descriptor.ObjectCapabilities[0].Operations = []string{"list"}
	descriptor.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore}
	if err := validateStoreOrganizationSnapshotOutput(action, descriptor); err == nil {
		t.Fatal("snapshot with a side-effecting Organization capability was accepted")
	}
}
