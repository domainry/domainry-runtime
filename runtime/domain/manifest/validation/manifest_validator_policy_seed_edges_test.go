package validation

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestValidateMaterializedSeedRecordsEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "seeded"},
			{Key: "optional"},
			{Key: "runtime", Config: map[string]any{"runtime_owned": true}},
			{Key: "unseeded"},
		},
		IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "optional"}},
	}
	state := newValidationState(manifest, nil)
	state.validateMaterializedSeedRecords([]businessseedmodel.BusinessSeedProvenance{
		{ObjectKey: "identity_user"},
		{ObjectKey: "missing", SeedKey: "missing", RecordID: "record", ContentHash: "hash"},
		{ObjectKey: "seeded", RecordID: "record", ContentHash: "hash"},
		{ObjectKey: "seeded", SeedKey: "seed", ContentHash: "hash"},
		{ObjectKey: "seeded", SeedKey: "seed", RecordID: "record"},
		{ObjectKey: "seeded", SeedKey: "seed", RecordID: "record", ContentHash: "hash"},
	})
	if len(state.errs) < 5 {
		t.Fatalf("materialized seed diagnostics = %#v", state.errs)
	}

	empty := newValidationState(manifestmodel.ManifestSchema{}, nil)
	empty.validateMaterializedSeedRecords(nil)
	if len(empty.errs) != 1 {
		t.Fatalf("empty materialized seed diagnostics = %#v", empty.errs)
	}
}
