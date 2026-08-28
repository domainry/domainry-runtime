package openapi

import (
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestHighRiskOwnerRoutesPublishIdempotencyAndReceiptHeaders(t *testing.T) {
	document := Build(metadatamodel.MetadataSchemaSnapshot{})
	paths := document["paths"].(map[string]any)
	for _, item := range []struct{ path, method string }{
		{path: "/operations/scheduler/runs/{runID}/retry", method: "post"},
		{path: "/operations/workflow/processes/{processID}/retry", method: "post"},
		{path: "/operations/integrations/events/{eventID}/replay", method: "post"},
		{path: "/operations/lifecycle/cleanup/jobs/{jobID}/run", method: "post"},
		{path: "/operations/idempotency/receipts/{owner}/{receiptID}/retry", method: "post"},
	} {
		operation := paths[item.path].(map[string]any)[item.method].(map[string]any)
		parameters := operation["parameters"].([]map[string]any)
		foundKey := false
		for _, parameter := range parameters {
			if parameter["name"] == "Idempotency-Key" && parameter["in"] == "header" && parameter["required"] == true {
				foundKey = true
			}
		}
		if !foundKey {
			t.Errorf("%s %s is missing required Idempotency-Key", item.method, item.path)
		}
		response := operation["responses"].(map[string]any)["200"].(map[string]any)
		headers := response["headers"].(map[string]any)
		for _, name := range []string{"Location", "Operation-ID", "Operation-Location", "Idempotency-Replayed"} {
			if _, found := headers[name]; !found {
				t.Errorf("%s %s response missing %s", item.method, item.path, name)
			}
		}
	}
}
