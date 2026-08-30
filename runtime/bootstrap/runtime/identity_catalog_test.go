package runtime

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

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
