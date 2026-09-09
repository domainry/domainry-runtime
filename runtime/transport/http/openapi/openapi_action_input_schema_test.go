package openapi

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionOpenAPIRequestBodyIsDerivedFromStructuredPayloadFields(t *testing.T) {
	two := 2
	action := definitionmodel.ActionSchema{Key: "register_accounts", ObjectKey: "customer", Kind: "bulk_operation", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "name", Name: "Name", Type: "text", Required: true},
		{Key: "accounts", Name: "Accounts", Type: "object", Repeated: true, Required: true, MaxItems: &two, Fields: []definitionmodel.ActionPayloadField{
			{Key: "bank", Type: "text", Required: true}, {Key: "primary", Type: "boolean"},
		}},
		{Key: "assignees", Type: "user", Repeated: true, Description: "Reviewers"},
		{Key: "kind", Type: "select", Options: []string{"a", "b"}},
	}}
	paths := map[string]any{}
	addActionOpenAPIPath(paths, action)
	requestSchema := func(path string) map[string]any {
		t.Helper()
		operation := paths[path].(map[string]any)["post"].(map[string]any)
		body := operation["requestBody"].(map[string]any)
		return body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	}
	for _, path := range []string{"/records/customer/actions/register_accounts", "/records/customer/items/{recordID}/actions/register_accounts", "/records/customer/actions/register_accounts/bulk"} {
		schema := requestSchema(path)
		if schema["$ref"] != nil {
			t.Fatalf("%s still uses the untyped request: %#v", path, schema)
		}
		data := schema["properties"].(map[string]any)["data"].(map[string]any)
		if data["additionalProperties"] != false || !reflect.DeepEqual(data["required"], []string{"accounts", "name"}) {
			t.Fatalf("%s data schema=%#v", path, data)
		}
		properties := data["properties"].(map[string]any)
		accounts := properties["accounts"].(map[string]any)
		if accounts["type"] != "array" || accounts["minItems"] != 1 || accounts["maxItems"] != 2 || accounts["title"] != "Accounts" {
			t.Fatalf("accounts array=%#v", accounts)
		}
		item := accounts["items"].(map[string]any)
		if item["additionalProperties"] != false || !reflect.DeepEqual(item["required"], []string{"bank"}) || item["title"] != nil {
			t.Fatalf("accounts item=%#v", item)
		}
		if item["properties"].(map[string]any)["primary"].(map[string]any)["type"] != "boolean" {
			t.Fatalf("nested leaf=%#v", item["properties"])
		}
		assignees := properties["assignees"].(map[string]any)
		if assignees["type"] != "array" || assignees["maxItems"] != definitionmodel.ActionPayloadMaxItems || assignees["minItems"] != nil || assignees["description"] != "Reviewers" || assignees["items"].(map[string]any)["type"] != "string" {
			t.Fatalf("assignees array=%#v", assignees)
		}
		if !reflect.DeepEqual(properties["kind"].(map[string]any)["enum"], []string{"a", "b"}) {
			t.Fatalf("select enum=%#v", properties["kind"])
		}
		for _, extra := range []string{"expected_version", "expected_updated_at", "record_id", "request_ref", "approved", "approval_id", "approval_token"} {
			if properties[extra] == nil {
				t.Fatalf("invocation extra %s missing from %#v", extra, properties)
			}
		}
	}
	if requestSchema("/records/customer/actions/register_accounts/bulk")["properties"].(map[string]any)["record_ids"] == nil {
		t.Fatal("bulk request lost record_ids")
	}
	if requestSchema("/records/customer/actions/register_accounts")["properties"].(map[string]any)["target_organization_id"] == nil {
		t.Fatal("object request lost target_organization_id")
	}

	legacy := map[string]any{}
	addActionOpenAPIPath(legacy, definitionmodel.ActionSchema{Key: "qualify", ObjectKey: "lead", Kind: "record_operation"})
	operation := legacy["/records/lead/items/{recordID}/actions/qualify"].(map[string]any)["post"].(map[string]any)
	schema := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["$ref"] != "#/components/schemas/ActionRequest" {
		t.Fatalf("legacy action without payload contract must keep the shared request: %#v", schema)
	}
}
