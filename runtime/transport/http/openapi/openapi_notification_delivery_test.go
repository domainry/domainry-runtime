package openapi_test

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	openapihttp "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
)

func TestNotificationDeliveryOpenAPIExposesFiltersAndProjection(t *testing.T) {
	paths := openapihttp.Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	operation := paths["/notification/deliveries"].(map[string]any)["get"].(map[string]any)
	if operation["operationId"] != "GetNotificationDeliveries" {
		t.Fatalf("operationId = %v", operation["operationId"])
	}
	parameters := operation["parameters"].([]map[string]any)
	parameterNames := map[string]bool{}
	for _, parameter := range parameters {
		parameterNames[parameter["name"].(string)] = true
	}
	if !parameterNames["status"] || !parameterNames["limit"] {
		t.Fatalf("delivery filters = %#v", parameterNames)
	}
	responses := operation["responses"].(map[string]any)
	content := responses["200"].(map[string]any)["content"].(map[string]any)
	schema := content["application/json"].(map[string]any)["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if properties["deliveries"] == nil || properties["count"] == nil {
		t.Fatalf("delivery response properties = %#v", properties)
	}
	delivery := properties["deliveries"].(map[string]any)["items"].(map[string]any)
	deliveryProperties := delivery["properties"].(map[string]any)
	for _, key := range []string{"id", "connector_key", "operation", "status", "attempt_count", "payload"} {
		if deliveryProperties[key] == nil {
			t.Errorf("delivery response is missing %s", key)
		}
	}
}
