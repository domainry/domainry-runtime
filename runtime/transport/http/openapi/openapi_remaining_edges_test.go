package openapi

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"net/http"
	"net/http/httptest"
	"testing"

	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type openAPISchemaProvider struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (p openAPISchemaProvider) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return p.snapshot
}

func TestOpenAPIPrimitiveHelperRemainingEdges(t *testing.T) {
	operation := openAPIOperation("edge", "", "edge", openAPISecurity{})
	if _, exists := operation["security"]; exists {
		t.Fatal("nil security items must not publish an implicit security override")
	}
	if got := openAPIOperationName(" -- "); got != "Resource" {
		t.Fatalf("empty operation name=%q", got)
	}
	if got := openAPIConfigString(nil, "key"); got != "" {
		t.Fatalf("nil config=%q", got)
	}
	if got := openAPIConfigString(map[string]any{}, "key"); got != "" {
		t.Fatalf("missing config=%q", got)
	}
	if got := openAPIConfigString(map[string]any{"key": nil}, "key"); got != "" {
		t.Fatalf("nil config value=%q", got)
	}
	if got := openAPIConfigString(map[string]any{"key": " value "}, "key"); got != "value" {
		t.Fatalf("config value=%q", got)
	}
	if properties := openAPIObject(map[string]any{"id": map[string]any{"type": "string"}})["properties"].(map[string]any); properties["id"] == nil {
		t.Fatalf("properties=%v", properties)
	}
}

func TestAddBuilderPathCoversExistingMalformedAndAllBodyMethods(t *testing.T) {
	paths := map[string]any{}
	addBuilderPath(paths, "/items/{itemID", "Items", "get")
	operation := paths["/items/{itemID"].(map[string]any)["get"].(map[string]any)
	if _, exists := operation["parameters"]; exists {
		t.Fatalf("unterminated path parameter was published: %v", operation)
	}

	addBuilderPath(paths, "/items/{itemID}", "Items", "post", "put", "patch", "get")
	path := paths["/items/{itemID}"].(map[string]any)
	for _, method := range []string{"post", "put", "patch"} {
		if path[method].(map[string]any)["requestBody"] == nil {
			t.Fatalf("%s request body missing", method)
		}
	}
	if path["get"].(map[string]any)["requestBody"] != nil {
		t.Fatal("GET unexpectedly has request body")
	}
	previousID := path["get"].(map[string]any)["operationId"]
	addBuilderPath(paths, "/items/{itemID}", "Items", "get")
	if path["get"].(map[string]any)["operationId"] != previousID {
		t.Fatal("existing operation was replaced")
	}
}

func TestObjectActionPathsAndConnectorProjectionCannotPublishOwnerRoute(t *testing.T) {
	paths := map[string]any{}
	addObjectOpenAPIPaths(paths, definitionmodel.ObjectSchema{})
	if len(paths) != 0 {
		t.Fatalf("empty object paths=%v", paths)
	}
	addActionOpenAPIPath(paths, definitionmodel.ActionSchema{ObjectKey: "object"})
	addActionOpenAPIPath(paths, definitionmodel.ActionSchema{Key: "action"})
	if len(paths) != 0 {
		t.Fatalf("incomplete action paths=%v", paths)
	}

	addActionOpenAPIPath(paths, definitionmodel.ActionSchema{ObjectKey: "object", Key: "create", Kind: "object_create"})
	if paths["/objects/object/actions/create/run"] == nil || paths["/objects/object/records/{recordID}/actions/create"] != nil {
		t.Fatalf("object-only paths=%v", paths)
	}
	addActionOpenAPIPath(paths, definitionmodel.ActionSchema{ObjectKey: "object", Key: "bulk", Kind: "bulk_operation"})
	if paths["/objects/object/actions/bulk/run"] == nil || paths["/objects/object/records/{recordID}/actions/bulk"] == nil {
		t.Fatalf("bulk paths=%v", paths)
	}
	addActionOpenAPIPath(paths, definitionmodel.ActionSchema{ObjectKey: "object", Key: "record", Kind: "record_operation"})
	if paths["/objects/object/actions/record/run"] != nil || paths["/objects/object/records/{recordID}/actions/record"] == nil {
		t.Fatalf("record paths=%v", paths)
	}

	spec := Build(appschemamodel.ApplicationSchemaSnapshot{Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{
		{Key: "empty"},
		{Key: "webhook", Name: "Webhook"},
	}}})
	paths = spec["paths"].(map[string]any)
	if paths["/integrations/webhooks/{workspaceID}/{connectionKey}"] != nil {
		t.Fatal("Integration-owner webhook route leaked into Runtime OpenAPI")
	}
}

func TestOpenAPIObjectDataSchemaCoversFilteringAndValidation(t *testing.T) {
	minimum, maximum := 1.5, 9.5
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: " ", Type: "string"},
		{Key: "disabled", Type: "string", DisabledAt: "2026-07-20T00:00:00Z"},
		{Key: "plain", Type: "string"},
		{Key: "amount", Name: "Amount", Type: "number", Required: true, Validation: definitionmodel.FieldValidation{
			MinLength: 1, MaxLength: 8, Min: &minimum, Max: &maximum, Pattern: "^[0-9]+$", Options: []string{"1", "2"},
		}},
	}}
	schema := openAPIObjectDataSchema(object)
	properties := schema["properties"].(map[string]any)
	if len(properties) != 2 || properties["plain"] == nil || properties["amount"] == nil {
		t.Fatalf("properties=%v", properties)
	}
	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "amount" {
		t.Fatalf("required=%v", required)
	}
	amount := properties["amount"].(map[string]any)
	for _, key := range []string{"minLength", "maxLength", "minimum", "maximum", "pattern", "enum", "title"} {
		if amount[key] == nil {
			t.Fatalf("amount %s missing: %v", key, amount)
		}
	}
	plain := properties["plain"].(map[string]any)
	if plain["title"] != nil {
		t.Fatalf("unnamed field title=%v", plain["title"])
	}
	if empty := openAPIObjectDataSchema(definitionmodel.ObjectSchema{}); empty["required"] != nil {
		t.Fatalf("empty required=%v", empty["required"])
	}
}

func TestOpenAPIFieldValidationOptionsAreIndependentlyOptional(t *testing.T) {
	minimum, maximum := 1.0, 2.0
	tests := []definitionmodel.FieldSchema{
		{Key: "none", Type: "string"},
		{Key: "min_length", Type: "string", Validation: definitionmodel.FieldValidation{MinLength: 1}},
		{Key: "max_length", Type: "string", Validation: definitionmodel.FieldValidation{MaxLength: 2}},
		{Key: "minimum", Type: "number", Validation: definitionmodel.FieldValidation{Min: &minimum}},
		{Key: "maximum", Type: "number", Validation: definitionmodel.FieldValidation{Max: &maximum}},
		{Key: "pattern", Type: "string", Validation: definitionmodel.FieldValidation{Pattern: "x"}},
		{Key: "options", Type: "string", Validation: definitionmodel.FieldValidation{Options: []string{"x"}}},
		{Key: "title", Name: "Title", Type: "string"},
	}
	for _, field := range tests {
		if schema := openAPIFieldSchema(field); schema["type"] == nil {
			t.Fatalf("field %s schema=%v", field.Key, schema)
		}
	}
	for _, fieldType := range []string{"integer", "boolean", "date", "json", "array"} {
		if schema := openAPIFieldSchema(definitionmodel.FieldSchema{Key: fieldType, Type: fieldType}); schema["type"] == nil {
			t.Fatalf("type %s schema=%v", fieldType, schema)
		}
	}
	decimalSchema := openAPIFieldSchema(definitionmodel.FieldSchema{Key: "amount", Type: "currency"})
	if decimalSchema["type"] != "string" || decimalSchema["format"] != "decimal" || decimalSchema["x-lossless-decimal"] != true || decimalSchema["pattern"] == "" {
		t.Fatalf("currency schema=%v", decimalSchema)
	}
}

func TestOpenAPIHandlerPublishesSnapshotHeadersAndRouteContract(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{TemplateID: "runtime", SchemaHash: "schema-hash"}
	service := appschemaapplication.NewApplicationSchemaQueryApplicationService(openAPISchemaProvider{snapshot: snapshot}, nil)
	var status int
	var value any
	handler := NewOpenAPIHandler(OpenAPIDependencies{Schema: service, ProductBrandName: "Acme", WriteJSON: func(_ http.ResponseWriter, gotStatus int, gotValue any) {
		status, value = gotStatus, gotValue
	}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	writer := httptest.NewRecorder()
	mux.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if status != http.StatusOK || value == nil {
		t.Fatalf("status=%d value=%v", status, value)
	}
	if got := writer.Header().Get("ETag"); got != `"schema-hash-openapi-`+productbrand.NameRevision("Acme")+`"` {
		t.Fatalf("etag=%q", got)
	}
	if got := writer.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("cache control=%q", got)
	}

	writer = httptest.NewRecorder()
	mux.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/openapi.json", nil))
	if writer.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", writer.Code)
	}
}

func TestOpenAPIDefaultTitleUsesProductBrand(t *testing.T) {
	defaultSpec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	if got := defaultSpec["info"].(map[string]any)["title"]; got != "Generated Domainry API" {
		t.Fatalf("default title=%v", got)
	}
	overridden := BuildWithProductBrand(appschemamodel.ApplicationSchemaSnapshot{}, " Acme ")
	if got := overridden["info"].(map[string]any)["title"]; got != "Generated Acme API" {
		t.Fatalf("overridden title=%v", got)
	}
}

func TestOwnerReceiptContractsIgnoreMalformedEntries(t *testing.T) {
	paths := map[string]any{
		"/operations/scheduler/definitions/{definitionID}/run": "not-a-path-item",
		"/operations/scheduler/runs/{runID}/retry":             map[string]any{"post": "not-an-operation"},
		"/operations/scheduler/runs/{runID}/cancel": map[string]any{"post": map[string]any{
			"responses": map[string]any{"default": map[string]any{}, "200": "not-a-response"},
		}},
	}
	addOwnerOperationsReceiptOpenAPIContracts(paths)
	operation := paths["/operations/scheduler/runs/{runID}/cancel"].(map[string]any)["post"].(map[string]any)
	parameters := operation["parameters"].([]map[string]any)
	if len(parameters) != 3 {
		t.Fatalf("parameters=%v", parameters)
	}
}
