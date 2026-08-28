package runtime

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type seedScopeOwnerWorkforceDirectory struct {
	runtimeIdentityDirectoryStub
	entries []identitysdk.WorkforceEntry
}

func (d seedScopeOwnerWorkforceDirectory) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return d.entries, nil
}

func TestRuntimeBusinessSeedsDeriveScopeOwnerDepartment(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{
			Key: "service_request",
			Fields: []definitionmodel.FieldSchema{
				{Key: "assignee_id", Type: "user", Config: map[string]any{"scope_owner": true}},
				{Key: "owner_department_id", Type: "text"},
				{Key: "owner_department_path", Type: "text"},
			},
		}},
		SeedRecords: []businessseedmodel.SeedRecordSchema{{
			ObjectKey: "service_request",
			Data: map[string]any{
				"__seed_key":            "request-1",
				"assignee_id":           "technician",
				"owner_department_id":   "spoofed",
				"owner_department_path": "/spoofed",
			},
		}},
	}

	derived, err := deriveRuntimeManifestScopeOwnerSeeds(t.Context(), "default", manifest, seedScopeOwnerWorkforceDirectory{entries: []identitysdk.WorkforceEntry{{
		IdentityUserID: "technician", OrganizationUnitID: "field-ops", OrganizationPath: "/operations/field-ops",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	data := derived.SeedRecords[0].Data
	if data["owner_department_id"] != "field-ops" || data["owner_department_path"] != "/operations/field-ops" {
		t.Fatalf("derived scope owner department=%#v", data)
	}
	if manifest.SeedRecords[0].Data["owner_department_id"] != "spoofed" {
		t.Fatalf("source manifest was mutated=%#v", manifest.SeedRecords[0].Data)
	}
}
