package runtime

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRuntimeIdentityCatalogPublishesUniversalFactForPermissionOnlyResource(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
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

func TestRuntimeIdentityCatalogDeduplicatesAuthoredSystemFields(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{
		Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "id"}, {Key: "created_at"}, {Key: "name"}},
	}}}
	catalog := runtimeIdentityCatalog(snapshot, "default", "runtime", nil)
	if err := catalog.ValidateContract(); err != nil {
		t.Fatalf("catalog validation: %v", err)
	}
	for _, resource := range catalog.Resources {
		if resource.Key != "customer" {
			continue
		}
		counts := map[string]int{}
		for _, field := range resource.Fields {
			counts[field]++
		}
		if counts["id"] != 1 || counts["created_at"] != 1 || counts["name"] != 1 {
			t.Fatalf("deduplicated fields = %#v", resource.Fields)
		}
		return
	}
	t.Fatal("customer resource was not published")
}
