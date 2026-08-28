package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestScopeOwnerContractAppliesWithoutDepartmentPermission(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "report_export_audit",
		Fields: []definitionmodel.FieldSchema{{
			Key: "requester_user_id", Type: "user",
			Config: map[string]any{"scope_owner": true, "auto_assign_current_user": true},
		}},
	}}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if err := state.errs.Error(); !strings.Contains(err, "owner_department_id") || !strings.Contains(err, "owner_department_path") {
		t.Fatalf("missing Runtime scope-owner fields were accepted: %v", state.errs)
	}
}

func TestManifestScopeOwnerContractAggregatesInvalidShape(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "service_request",
		Fields: []definitionmodel.FieldSchema{
			{Key: "requester", Type: "text", Config: map[string]any{"scope_owner": true}},
			{Key: "assignee", Type: "user", Config: map[string]any{"scope_owner": true, "auto_assign_current_user": "yes"}},
			{Key: "owner_department_id", Type: "integer"},
		},
	}}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	err := state.errs.Error()
	for _, expected := range []string{"exactly one scope_owner", "must mark a user field", "auto_assign_current_user", "owner_department_path", `must be text; found "integer"`} {
		if !strings.Contains(err, expected) {
			t.Fatalf("missing %q in aggregated manifest diagnostics: %v", expected, state.errs)
		}
	}
}

func TestManifestScopeOwnerAcceptsRuntimeRecognizedNamesAndRelation(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "service_request",
		Fields: []definitionmodel.FieldSchema{
			{Key: "requester", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_user"}, Config: map[string]any{"scope_owner": true}},
			{Key: "ownerDepartmentId", Type: "text"},
			{Key: "ownerDepartmentPath", Type: "text"},
		},
	}}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if len(state.errs) != 0 {
		t.Fatalf("valid Runtime-recognized scope-owner contract rejected: %v", state.errs)
	}
}
