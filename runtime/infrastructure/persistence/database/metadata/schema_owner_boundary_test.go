package metadata

import (
	"os"
	"strings"
	"testing"
)

func TestMetadataRepositoryOwnerContainsNoSchemaMutation(t *testing.T) {
	source, err := os.ReadFile("metadata_store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "NewCreateTableBuilder", "NewAddColumnBuilder"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("metadata repository owner contains schema mutation %q", forbidden)
		}
	}
}

func TestMetadataSchemaMaterializerOwnsDynamicPhysicalSchema(t *testing.T) {
	source, err := os.ReadFile("schema_materializer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"MigrationPlan", "SyncManifest", "ensureObjectStorage", "NewCreateTableBuilder", "NewAddColumnBuilder"} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("metadata schema materializer is missing %q", required)
		}
	}
}
