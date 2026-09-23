package runtimehost

import (
	"slices"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
	databaseschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

func TestRuntimeSchemaOwnershipPublishesTheSourceOwnedContract(t *testing.T) {
	got := RuntimeSchemaOwnership()
	want := databaseschema.RuntimeSchemaOwnership()
	if err := schemaownership.ValidateAll(got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(schemaownership.Names(got), schemaownership.Names(want)) {
		t.Fatalf("runtime ownership names=%v want=%v", schemaownership.Names(got), schemaownership.Names(want))
	}
	got[0].PrimaryKey[0] = "changed"
	if slices.Equal(got[0].PrimaryKey, RuntimeSchemaOwnership()[0].PrimaryKey) {
		t.Fatal("runtime ownership publication leaked mutable state")
	}
}
