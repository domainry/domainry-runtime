package appschema

import (
	"os"
	"strings"
	"testing"
)

func TestMetadataRepositoryOwnerContainsNoSchemaMutation(t *testing.T) {
	source, err := os.ReadFile("application_schema_store.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "recordschema.NewTable", "ormschema.NewAddColumn"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("metadata repository owner contains schema mutation %q", forbidden)
		}
	}
}

func TestApplicationSchemaMaterializerOwnsDynamicPhysicalSchema(t *testing.T) {
	source, err := os.ReadFile("schema_materializer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"MigrationPlan", "SyncManifest", "ensureObjectStorage", "recordschema.NewTable", "ormschema.NewAddColumn"} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("metadata schema materializer is missing %q", required)
		}
	}
}

// The upgrade plan only reads the physical schema, and upgrade execution only
// writes receipt DML: business DDL stays inside ensureObjectStorage, and the
// receipt ledger itself is created by the host schema migration (025).
func TestUpgradePlanAndExecutionDoNotOwnPhysicalDDL(t *testing.T) {
	for _, file := range []string{"schema_upgrade_plan.go", "schema_upgrade_execution.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "recordschema.NewTable", "ormschema.NewAddColumn", "ormschema.NewTable"} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("%s contains physical schema mutation %q", file, forbidden)
			}
		}
	}
	plan, err := os.ReadFile("schema_upgrade_plan.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"UpgradePlan", "planObjectColumns", "uniqueDuplicateGroups", "retainedUpgradeSteps"} {
		if !strings.Contains(string(plan), required) {
			t.Fatalf("upgrade plan is missing %q", required)
		}
	}
	materializer, err := os.ReadFile("schema_materializer.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"runReceiptedUpgradeStep", "writeUpgradeReceipt", "FieldBackfillValue"} {
		if !strings.Contains(string(materializer), required) {
			t.Fatalf("materializer no longer drives column additions and backfills through receipts: missing %q", required)
		}
	}
}
