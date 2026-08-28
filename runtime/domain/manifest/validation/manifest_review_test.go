package validation

import (
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"encoding/json"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestReviewManifestUpdateBlocksDestructiveChangesWithoutApproval(t *testing.T) {
	previous := loadFixtureManifest(t, "crm-customer-360.json")
	tests := []struct {
		name string
		next func(manifestmodel.ManifestSchema) manifestmodel.ManifestSchema
		want string
	}{
		{
			name: "remove object",
			next: func(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				manifest.Objects = manifest.Objects[:1]
				return manifest
			},
			want: "remove_object",
		},
		{
			name: "remove field",
			next: func(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				manifest.Objects[0].Fields = manifest.Objects[0].Fields[:2]
				return manifest
			},
			want: "remove_field",
		},
		{
			name: "change field type",
			next: func(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				manifest.Objects[0].Fields[3].Type = "text"
				return manifest
			},
			want: "change_field_type",
		},
		{
			name: "add required field without default",
			next: func(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				manifest.Objects[0].Fields = append(manifest.Objects[0].Fields, definitionmodel.FieldSchema{Key: "tax_id", Name: "Tax ID", Type: "text", Required: true})
				return manifest
			},
			want: "add_required_without_default",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := tt.next(cloneManifestForReviewTest(t, previous))
			result := ReviewManifestUpdate(previous, next, ReviewOptions{})
			if !result.HasBlockers() {
				t.Fatalf("ReviewManifestUpdate() expected blocker %q, got none", tt.want)
			}
			if got := reviewKinds(result.Blockers); !strings.Contains(got, tt.want) {
				t.Fatalf("ReviewManifestUpdate() blockers = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestReviewManifestUpdateKeepsApprovedDestructiveChangesAuditable(t *testing.T) {
	previous := loadFixtureManifest(t, "crm-customer-360.json")
	next := cloneManifestForReviewTest(t, previous)
	next.Objects[0].Fields[3].Type = "text"
	result := ReviewManifestUpdate(previous, next, ReviewOptions{ApproveDestructive: true})
	if result.HasBlockers() {
		t.Fatalf("ReviewManifestUpdate() blockers = %+v, want none with approval", result.Blockers)
	}
	if len(result.Changes) == 0 || !result.Changes[0].Approved || !result.Changes[0].Destructive {
		t.Fatalf("ReviewManifestUpdate() changes = %+v, want approved destructive audit change", result.Changes)
	}
}

func TestValidateManifestUpdateCombinesSchemaAndDestructiveChecks(t *testing.T) {
	previous := loadFixtureManifest(t, "crm-customer-360.json")
	next := cloneManifestForReviewTest(t, previous)
	next.Objects[0].Fields[3].Type = "text"
	if err := ValidateManifestUpdate(previous, next, ReviewOptions{}); err == nil || !strings.Contains(err.Error(), "field type") {
		t.Fatalf("ValidateManifestUpdate() error = %v, want destructive review error", err)
	}
}

func cloneManifestForReviewTest(t *testing.T, manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var clone manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("unmarshal manifest clone: %v", err)
	}
	return clone
}

func reviewKinds(changes []ReviewChange) string {
	kinds := make([]string, 0, len(changes))
	for _, change := range changes {
		kinds = append(kinds, change.Kind)
	}
	return strings.Join(kinds, ",")
}
