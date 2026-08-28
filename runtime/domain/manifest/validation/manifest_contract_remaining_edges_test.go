package validation

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestBusinessIdentityBindingRemainingInvalidShapes(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "profile", Fields: []definitionmodel.FieldSchema{{Key: "", Type: "text"}},
	}}}, nil)
	state.validateBusinessIdentityBinding("binding", profilebindingmodel.Binding{
		ObjectKey: "profile",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
			Key: "identity", SurfaceKeys: []string{" "}, StatusField: "missing", BlacklistField: "missing",
			Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: " ", FieldKey: " "}},
		},
	})
	if len(state.errs) < 6 {
		t.Fatalf("diagnostics=%v", state.errs)
	}
}

func TestManifestConditionalUniqueRemainingInvalidShapes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "", Type: "text"}, {Key: "known", Type: "text"}}}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}, nil)
	state.validateConditionalUnique(object, definitionmodel.ValidationSchema{
		Fields: []string{" ", "missing"},
		Config: map[string]any{"condition_field": " ", "condition_values": []any{"", " padded "}},
	}, "conditional")
	if len(state.errs) < 4 {
		t.Fatalf("diagnostics=%v", state.errs)
	}
}

func TestManifestRelatedAggregateRemainingInvalidShapes(t *testing.T) {
	target := definitionmodel.ObjectSchema{Key: "target", Fields: []definitionmodel.FieldSchema{{Key: "text_limit", Type: "text"}, {Key: "currency_limit", Type: "currency"}, {Key: "number_limit", Type: "number"}}}
	object := definitionmodel.ObjectSchema{Key: "item", Fields: []definitionmodel.FieldSchema{
		{Key: "", Type: "text"},
		{Key: "empty_target", Type: "relation"},
		{Key: "unknown_target", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "missing"}},
		{Key: "target_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "target"}},
		{Key: "amount", Type: "currency"},
	}}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{target, object}}, nil)
	configs := []map[string]any{
		{"relation_field": "missing"},
		{"relation_field": "empty_target"},
		{"relation_field": "unknown_target"},
		{"relation_field": "target_id", "aggregate": "sum", "value_field": "missing", "limit_field": "text_limit", "operator": "lte"},
		{"relation_field": "target_id", "aggregate": "sum", "value_field": "missing", "limit_field": "currency_limit", "operator": "lte"},
		{"relation_field": "target_id", "aggregate": "count", "limit_field": "currency_limit", "operator": "lte"},
		{"relation_field": "target_id", "aggregate": "count", "limit_field": "number_limit", "operator": "lte", "included_statuses": []any{"active"}, "status_field": ""},
	}
	for _, config := range configs {
		state.validateRelatedAggregateInvariant(object, definitionmodel.ValidationSchema{Config: config}, "aggregate")
	}
	if len(state.errs) < 6 {
		t.Fatalf("diagnostics=%v", state.errs)
	}
}

func TestManifestRequiredIndexAndMapSliceRemainingShapes(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "known", Type: "text"}}}}}, nil)
	state.requireManifestFieldIndex("", "known", "path", "filter")
	state.requireManifestFieldIndex("item", "", "path", "filter")
	state.requireManifestFieldIndex("item", "missing", "path", "filter")
	state.fields["item"]["blank"] = definitionmodel.FieldSchema{}
	state.requireManifestFieldIndex("item", "blank", "path", "filter")
	if values := manifestMapSlice([]any{"not-a-map", map[string]any{"field": "known"}}); len(values) != 1 {
		t.Fatalf("values=%v", values)
	}
	if values := manifestMapSlice([]map[string]any{{"field": "known"}}); len(values) != 1 {
		t.Fatalf("typed values=%v", values)
	}
	if manifestFieldIndexed(definitionmodel.FieldSchema{Config: map[string]any{"indexed": " true "}}) != true {
		t.Fatal("string index flag not accepted")
	}
	if manifestFieldIndexed(definitionmodel.FieldSchema{Config: map[string]any{"indexed": 1}}) {
		t.Fatal("numeric index flag unexpectedly accepted")
	}
}

func TestManifestConditionalUniqueRawListAndRemainingValidatorEdges(t *testing.T) {
	if values := conditionalUniqueStringList([]string{" raw "}); len(values) != 1 || values[0] != " raw " {
		t.Fatalf("values=%q", values)
	}

	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "item", Validations: []definitionmodel.ValidationSchema{{
			Type: "state_machine", Config: map[string]any{"transitions": []any{map[string]any{"patch": map[string]any{"status": "done"}}}},
		}}}},
		NotificationEventTypes: []notificationmodel.NotificationEventType{{}},
	}, nil)
	state.validateObjects()
	state.validateNotificationTemplates()
	if len(state.errs) == 0 {
		t.Fatal("invalid event type unexpectedly accepted")
	}

	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "item"}}}
	for index, value := range []string{"none", "failures", "all", "invalid"} {
		manifest.AutomationRules = append(manifest.AutomationRules, automationmodel.AutomationRuleSchema{
			Key: "rule-" + value, ObjectKey: "item",
			Trigger:   automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
			Execution: automationmodel.AutomationExecutionPolicy{ResultNotification: value},
		})
		_ = index
	}
	automationState := newValidationState(manifest, nil)
	automationState.validateAutomationRules()
	if len(automationState.errs) < 3 {
		t.Fatalf("automation diagnostics=%v", automationState.errs)
	}

	templates := manifestmodel.ManifestSchema{NotificationTemplates: []notificationmodel.NotificationTemplate{{Key: "other"}, {Key: "target"}}}
	if _, found := manifestNotificationTemplate(templates, "target"); !found {
		t.Fatal("target notification template not found")
	}
	if _, found := manifestNotificationTemplate(templates, "missing"); found {
		t.Fatal("missing notification template found")
	}
}

func TestManifestTemporalExclusionRemainingInvalidShapes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "booking", Fields: []definitionmodel.FieldSchema{{Key: "start", Type: "datetime"}, {Key: "scope", Type: "text"}}}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}, nil)
	state.validateTemporalExclusion(object, definitionmodel.ValidationSchema{Config: map[string]any{
		"start_field": "start", "end_field": "", "scope_fields": []any{"missing"}, "status_field": "missing",
	}}, "temporal")
	state.validateTemporalExclusion(object, definitionmodel.ValidationSchema{Config: map[string]any{
		"start_field": "start", "end_field": "missing", "scope_fields": []any{"scope"},
	}}, "temporal")
	if len(state.errs) < 4 {
		t.Fatalf("diagnostics=%v", state.errs)
	}
}
