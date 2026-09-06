package openapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestOpenAPIProjectsTypedStoreOrganizationSnapshotResponse(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{
		{Key: "store_config", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "service_rate", Type: "percent", Required: true}}},
		{Key: "floor_layout", Fields: []definitionmodel.FieldSchema{{Key: "layout", Type: "object", Required: true}}},
	}
	action := definitionmodel.ActionSchema{
		Key: "store_config.store_management_snapshot", ObjectKey: "store_config", Kind: "object_operation",
		OutputFields: []definitionmodel.ActionOutputField{{
			Key: "stores", Type: "store_organization_snapshot", Required: true,
			StoreOrganizationSnapshotObjectKeys: []string{"store_config", "floor_layout"},
		}},
	}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: objects, Actions: []definitionmodel.ActionSchema{action}}
	spec := Build(snapshot)
	first, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Build(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("typed snapshot OpenAPI contract is not deterministic")
	}
	path := spec["paths"].(map[string]any)["/records/store_config/actions/store_config.store_management_snapshot"]
	content, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		`"stores"`, `"items"`, `"next_cursor"`, `"organization"`, `"reference"`,
		`"code"`, `"name"`, `"status"`, `"sort_order"`, `"version"`,
		`"store_config"`, `"floor_layout"`, `"record_id"`, `"revision"`, `"data"`,
		`"x-domainry-opaque-reference":"identity.organization_unit"`,
		`"format":"decimal"`, `"x-lossless-decimal":true`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("typed snapshot OpenAPI is missing %s: %s", expected, text)
		}
	}
	if strings.Contains(text, "stores_json") {
		t.Fatalf("opaque JSON output leaked into typed snapshot OpenAPI: %s", text)
	}
}
