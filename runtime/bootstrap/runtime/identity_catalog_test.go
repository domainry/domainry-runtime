package runtime

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestRuntimeIdentityCatalogPublishesUniversalFactForPermissionOnlyResource(t *testing.T) {
	snapshot := metadatamodel.ApplicationSchemaSnapshot{
		EntryPoints: []definitionmodel.EntryPointSchema{{RequiredPermissions: []string{"workflow.task.act"}}},
	}
	catalog := runtimeIdentityCatalog(snapshot, "default", "runtime", nil)
	for _, resource := range catalog.Resources {
		if resource.Key != "workflow.task" {
			continue
		}
		for _, fact := range resource.SupportedFacts {
			if fact == "id" {
				return
			}
		}
		t.Fatalf("permission-only resource facts = %#v", resource.SupportedFacts)
	}
	t.Fatal("permission-only resource was not published")
}
