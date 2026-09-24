package database

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestRuntimeSchemaIdentityComesFromRenderedDDL(t *testing.T) {
	tests := []struct {
		name   string
		engine databaseEngine
		schema string
	}{
		{name: "sqlite", engine: sqlite.NewEngine()},
		{name: "mysql", engine: mysql.NewEngine()},
		{name: "postgres", engine: postgres.NewEngine(), schema: "domainry_runtime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &RuntimeStore{engine: test.engine, databaseSchema: test.schema}
			minimal, err := runtimeSchemaDDL(t.Context(), store, RuntimeSchemaCapabilities{})
			if err != nil {
				t.Fatal(err)
			}
			full, err := runtimeSchemaDDL(t.Context(), store, FullRuntimeSchemaCapabilities())
			if err != nil {
				t.Fatal(err)
			}
			minimalChecksum := runtimeSchemaChecksum(minimal)
			fullChecksum := runtimeSchemaChecksum(full)
			if len(minimalChecksum) != 64 || len(fullChecksum) != 64 || minimalChecksum == fullChecksum {
				t.Fatalf("schema checksums minimal=%q full=%q", minimalChecksum, fullChecksum)
			}
			if runtimeSchemaChecksum(append([]string(nil), full...)) != fullChecksum {
				t.Fatal("identical Runtime DDL produced a different checksum")
			}
			changed := append([]string(nil), full...)
			changed[len(changed)-1] += " "
			if runtimeSchemaChecksum(changed) != fullChecksum {
				t.Fatal("insignificant surrounding whitespace changed the Runtime DDL checksum")
			}
			changed[len(changed)-1] += "ADD COLUMN changed TEXT"
			if runtimeSchemaChecksum(changed) == fullChecksum {
				t.Fatal("changed Runtime DDL retained the previous checksum")
			}
			joined := strings.Join(full, "\n")
			if !strings.Contains(joined, "CREATE TABLE") || strings.Contains(joined, "001_runtime_schema") {
				t.Fatalf("Runtime schema identity is not canonical DDL: %s", joined)
			}
			if path := runtimeSchemaMigrationPath(fullChecksum); path != "runtime_schema_sha256_"+fullChecksum {
				t.Fatalf("content-addressed Runtime schema path=%q", path)
			}
		})
	}
}
