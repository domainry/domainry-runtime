package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestValidatesClosedStoreOrganizationSnapshotOutput(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "store_config", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}}
	validField := definitionmodel.ActionOutputField{Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config"}}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: objects, Actions: []definitionmodel.ActionSchema{{Key: "store_config.snapshot", ObjectKey: "store_config", Kind: definitionmodel.ActionKindObjectOperation, OutputFields: []definitionmodel.ActionOutputField{validField}}}}, nil)
	valid.validateActions()
	if len(valid.errs) != 0 {
		t.Fatalf("valid snapshot rejected: %v", valid.errs)
	}
	for name, field := range map[string]definitionmodel.ActionOutputField{
		"optional":       {Key: "stores", Type: "store_organization_snapshot", StoreOrganizationSnapshotObjectKeys: []string{"store_config"}},
		"missing object": {Key: "stores", Type: "store_organization_snapshot", Required: true},
		"unknown object": {Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"missing"}},
		"duplicate":      {Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config", "store_config"}},
		"wrong type":     {Key: "stores", Type: "text", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config"}},
	} {
		t.Run(name, func(t *testing.T) {
			state := newValidationState(manifestmodel.ManifestSchema{Objects: objects, Actions: []definitionmodel.ActionSchema{{Key: "store_config.snapshot", ObjectKey: "store_config", Kind: definitionmodel.ActionKindObjectOperation, OutputFields: []definitionmodel.ActionOutputField{field}}}}, nil)
			state.validateActions()
			if len(state.errs) == 0 || !strings.Contains(ValidationErrors(state.errs).Error(), "store_organization_snapshot") {
				t.Fatalf("unsafe snapshot accepted: %v", state.errs)
			}
		})
	}
}

func TestManifestRejectsMultipleStoreOrganizationSnapshotPages(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "store_config"}}
	field := definitionmodel.ActionOutputField{Key: "stores", Type: "store_organization_snapshot", Required: true, StoreOrganizationSnapshotObjectKeys: []string{"store_config"}}
	second := field
	second.Key = "more_stores"
	state := newValidationState(manifestmodel.ManifestSchema{Objects: objects, Actions: []definitionmodel.ActionSchema{{Key: "store_config.snapshot", ObjectKey: "store_config", Kind: definitionmodel.ActionKindObjectOperation, OutputFields: []definitionmodel.ActionOutputField{field, second}}}}, nil)
	state.validateActions()
	if len(state.errs) == 0 || !strings.Contains(ValidationErrors(state.errs).Error(), "only one store_organization_snapshot") {
		t.Fatalf("multiple snapshot pages accepted: %v", state.errs)
	}
}
