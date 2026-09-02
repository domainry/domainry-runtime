package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRejectsAuthoredScopeOwnerContract(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "service_request",
		Fields: []definitionmodel.FieldSchema{{
			Key: "requester", Type: "user", Config: map[string]any{"scope_owner": true},
		}},
	}}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if err := state.errs.Error(); !strings.Contains(err, "Runtime-owned") {
		t.Fatalf("authored scope_owner was accepted: %v", state.errs)
	}
}

func TestManifestDoesNotRequireOwnershipBusinessFields(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key:    "service_request",
		Fields: []definitionmodel.FieldSchema{{Key: "requester", Type: "user"}},
	}}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if len(state.errs) != 0 {
		t.Fatalf("Runtime-owned ownership columns leaked into object contract: %v", state.errs)
	}
}
