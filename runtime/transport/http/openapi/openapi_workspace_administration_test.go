package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestWorkspaceAdministrationOpenAPIUsesOnlyCanonicalReferencesAndClosedTypedSchemas(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/workspaces", "/workspaces/{workspaceCode}/suspend", "/workspaces/{workspaceCode}/reactivate", "/workspaces/{workspaceCode}/commercial-configuration",
	} {
		if paths[path] == nil {
			t.Fatalf("Workspace administration path %q is missing", path)
		}
	}
	raw, err := json.Marshal(map[string]any{
		"catalog":    paths["/workspaces"],
		"suspend":    paths["/workspaces/{workspaceCode}/suspend"],
		"reactivate": paths["/workspaces/{workspaceCode}/reactivate"],
		"commercial": paths["/workspaces/{workspaceCode}/commercial-configuration"],
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, forbidden := range []string{"workspace_id", "tenant", "registry", "mapping"} {
		if strings.Contains(strings.ToLower(contract), forbidden) {
			t.Fatalf("Workspace administration OpenAPI exposes forbidden %q: %s", forbidden, contract)
		}
	}
	for _, required := range []string{"canonical_code", "display_name", "status", "revision", "commercial_configuration", "expected_revision", "next_cursor", "Idempotency-Key"} {
		if !strings.Contains(contract, required) {
			t.Fatalf("Workspace administration OpenAPI omits %q: %s", required, contract)
		}
	}
	catalogOperation := paths["/workspaces"].(map[string]any)["get"].(map[string]any)
	catalogSchema := workspaceAdministrationResponseSchema(catalogOperation)
	assertClosedWorkspaceAdministrationObject(t, catalogSchema)
	assertClosedWorkspaceAdministrationEntry(t, catalogSchema["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any))

	for _, path := range []string{"/workspaces/{workspaceCode}/suspend", "/workspaces/{workspaceCode}/reactivate"} {
		operation := paths[path].(map[string]any)["post"].(map[string]any)
		assertWorkspaceAdministrationIdempotencyHeader(t, operation)
		assertClosedWorkspaceAdministrationObject(t, workspaceAdministrationRequestSchema(operation))
		responseSchema := workspaceAdministrationResponseSchema(operation)
		assertClosedWorkspaceAdministrationObject(t, responseSchema)
		assertClosedWorkspaceAdministrationEntry(t, responseSchema["properties"].(map[string]any)["workspace"].(map[string]any))
	}

	commercialOperation := paths["/workspaces/{workspaceCode}/commercial-configuration"].(map[string]any)["put"].(map[string]any)
	assertWorkspaceAdministrationIdempotencyHeader(t, commercialOperation)
	commercialRequest := workspaceAdministrationRequestSchema(commercialOperation)
	assertClosedWorkspaceAdministrationObject(t, commercialRequest)
	commercialRequestConfiguration := commercialRequest["properties"].(map[string]any)["commercial_configuration"].(map[string]any)
	assertClosedWorkspaceAdministrationObject(t, commercialRequestConfiguration)
	if commercialRequestConfiguration["properties"].(map[string]any)["revision"] != nil {
		t.Fatalf("commercial configuration exposes a second CAS revision: %#v", commercialRequestConfiguration)
	}
	commercialResponse := workspaceAdministrationResponseSchema(commercialOperation)
	assertClosedWorkspaceAdministrationObject(t, commercialResponse)
	assertClosedWorkspaceAdministrationEntry(t, commercialResponse["properties"].(map[string]any)["workspace"].(map[string]any))
}

func assertClosedWorkspaceAdministrationObject(t *testing.T, schema map[string]any) {
	t.Helper()
	if schema["additionalProperties"] != false {
		t.Fatalf("Workspace administration schema is open: %#v", schema)
	}
	if schema["properties"] == nil {
		t.Fatalf("Workspace administration schema has no typed properties: %#v", schema)
	}
}

func assertClosedWorkspaceAdministrationEntry(t *testing.T, schema map[string]any) {
	t.Helper()
	assertClosedWorkspaceAdministrationObject(t, schema)
	commercial := schema["properties"].(map[string]any)["commercial_configuration"].(map[string]any)
	assertClosedWorkspaceAdministrationObject(t, commercial)
	if commercial["properties"].(map[string]any)["revision"] != nil {
		t.Fatalf("commercial configuration exposes a second CAS revision: %#v", commercial)
	}
}

func assertWorkspaceAdministrationIdempotencyHeader(t *testing.T, operation map[string]any) {
	t.Helper()
	for _, parameter := range operation["parameters"].([]map[string]any) {
		if parameter["name"] == "Idempotency-Key" && parameter["in"] == "header" && parameter["required"] == true {
			return
		}
	}
	t.Fatalf("Workspace administration operation omits required Idempotency-Key: %#v", operation["parameters"])
}

func workspaceAdministrationRequestSchema(operation map[string]any) map[string]any {
	return operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
}

func workspaceAdministrationResponseSchema(operation map[string]any) map[string]any {
	return operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
}
