package openapi

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestBusinessAuditOpenAPIPublishesBoundedCursorPage(t *testing.T) {
	document := Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths, _ := document["paths"].(map[string]any)
	operation := openAPITestOperation(t, paths, "/business/audit-events", "get")
	if operation["operationId"] != "listBusinessAuditEventPage" || operation["x-domainry-runtime-client-method"] != "listBusinessAuditEventPage" {
		t.Fatalf("operation=%+v", operation)
	}
	for _, name := range []string{"object_key", "record_id", "event", "actor_id", "role_key", "request_id", "created_from", "created_to", "page_size", "cursor", "limit"} {
		if !openAPITestParameter(operation, "query", name) {
			t.Fatalf("missing query parameter %s", name)
		}
	}
	responses, _ := operation["responses"].(map[string]any)
	response, _ := responses["200"].(map[string]any)
	content, _ := response["content"].(map[string]any)
	jsonContent, _ := content["application/json"].(map[string]any)
	schema, _ := jsonContent["schema"].(map[string]any)
	required, _ := schema["required"].([]string)
	if len(required) != 6 {
		t.Fatalf("required=%v schema=%+v", required, schema)
	}
}
