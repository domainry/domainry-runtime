package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestValidateManifestChecksFieldUpgradeRules(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	manifest.SeedRecords = nil
	manifest.Objects[0].Fields = append(manifest.Objects[0].Fields,
		definitionmodel.FieldSchema{Key: "tier", Name: "Tier", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "standard"}},
		definitionmodel.FieldSchema{Key: "region", Name: "Region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}},
	)
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid upgrade rules rejected: %v", err)
	}
	manifest.Objects[0].Fields = append(manifest.Objects[0].Fields,
		definitionmodel.FieldSchema{Key: "level", Name: "Level", Type: "integer", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "many"}},
	)
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "objects[0].fields[4].upgrade: backend.metadata.field_upgrade_rule_invalid: object=customer field=level reason=backfill_value_invalid") {
		t.Fatalf("invalid upgrade rule error=%v", err)
	}
}
