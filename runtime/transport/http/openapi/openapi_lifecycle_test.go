package openapi

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestRuntimeOpenAPIHardcodesOnlyLifecycleCleanupOrchestration(t *testing.T) {
	paths := Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	operation := paths["/lifecycle/cleanup/jobs/{jobID}/run"].(map[string]any)["post"].(map[string]any)
	if security, _ := operation["security"].([]map[string]any); len(security) == 0 {
		t.Fatal("Runtime Lifecycle cleanup orchestration is public")
	}
	for _, path := range []string{"/lifecycle/policies", "/lifecycle/legal-holds", "/lifecycle/subjects", "/lifecycle/archive"} {
		if _, hardcoded := paths[path]; hardcoded {
			t.Fatalf("module-owned Lifecycle OpenAPI remained hardcoded: %s", path)
		}
	}
}
