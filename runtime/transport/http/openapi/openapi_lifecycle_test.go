package openapi

import (
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestOpenAPIExposesLifecycleManagementWithoutPublicSecurity(t *testing.T) {
	paths := Build(metadatamodel.MetadataSchemaSnapshot{})["paths"].(map[string]any)
	for _, path := range []string{"/operations/lifecycle/policies", "/operations/lifecycle/legal-holds", "/operations/lifecycle/legal-holds/{holdID}/end", "/operations/lifecycle/cleanup/preview", "/operations/lifecycle/cleanup/jobs/{jobID}/run", "/operations/lifecycle/metrics", "/operations/lifecycle/archive", "/operations/lifecycle/subjects/{requestID}/download", "/operations/lifecycle/external-erasures", "/operations/lifecycle/external-erasures/{erasureID}/reconcile", "/operations/lifecycle/deletions/replay"} {
		methods, ok := paths[path].(map[string]any)
		if !ok || len(methods) == 0 {
			t.Fatalf("missing lifecycle OpenAPI path %s", path)
		}
		for _, operationValue := range methods {
			operation := operationValue.(map[string]any)
			security, _ := operation["security"].([]map[string]any)
			if len(security) == 0 {
				t.Fatalf("lifecycle path %s is public", path)
			}
		}
	}
}
