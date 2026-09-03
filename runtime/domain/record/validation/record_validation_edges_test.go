package validation

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func recordValidationErrorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}

func TestRecordAccessValidationEdges(t *testing.T) {
	immutable := definitionmodel.ValidationSchema{Key: "lock", Type: "immutable_after_status", FieldKey: "amount", Config: map[string]any{"statuses": []string{"closed"}}}
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{immutable}}
	if err := RecordValidateImmutableAfterStatusPolicies(object, nil, nil, "update", principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if got := recordValidationErrorCode(RecordValidateImmutableAfterStatusPolicies(object, map[string]any{"status": "closed", "amount": 1}, map[string]any{"status": "closed", "amount": 2}, "update", principalmodel.Principal{})); got != "backend.policy.immutable_after_status" {
		t.Fatalf("got %s", got)
	}
	object.Validations[0].Fields = []string{"amount"}
	object.Validations[0].FieldKey = ""
	if err := RecordValidateImmutableAfterStatusPolicies(object, map[string]any{"status": "open", "amount": 1}, map[string]any{"status": "open", "amount": 2}, "update", principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	object.Validations[0].Fields = nil
	object.Validations[0].Config = map[string]any{"locked_statuses": []any{"closed"}, "fields": "amount", "operations": []string{"create"}}
	if err := RecordValidateImmutableAfterStatusPolicies(object, map[string]any{"status": "closed", "amount": 1}, map[string]any{"status": "closed", "amount": 2}, "update", principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}

	threshold := definitionmodel.ValidationSchema{Key: "limit", Type: "threshold_permission", FieldKey: "amount", Config: map[string]any{"threshold": 100, "permission": "order.approve"}}
	object.Validations = []definitionmodel.ValidationSchema{threshold}
	if got := recordValidationErrorCode(RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 101}, "update", principalmodel.Principal{})); got != "backend.policy.threshold_permission_required" {
		t.Fatalf("got %s", got)
	}
	permissions := []string{"order.approve"}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	if err := RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 101}, "update", principal); err != nil {
		t.Fatal(err)
	}
	object.Validations[0].Config["permission"] = ""
	if got := recordValidationErrorCode(RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 101}, "update", principal)); got != "backend.policy.permission_required" {
		t.Fatalf("got %s", got)
	}
	object.Validations[0].FieldKey = ""
	object.Validations[0].Fields = []string{"amount"}
	object.Validations[0].Config["field"] = ""
	if err := RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": "bad"}, "update", principal); err != nil {
		t.Fatal(err)
	}
	if values := firstConfiguredStringList(map[string]any{"second": []string{"x"}}, "first", "second"); len(values) != 1 || values[0] != "x" {
		t.Fatalf("%#v", values)
	}
}

func TestRecordContextAndRelatedEdges(t *testing.T) {
	policy := definitionmodel.ValidationSchema{Key: "context", Type: "relation_context", Fields: []string{"customer", "account"}}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "customer", Type: "relation", Config: map[string]any{"object_key": "customer"}}, {Key: "text", Type: "text"}}, Validations: []definitionmodel.ValidationSchema{policy}}
	if got := recordValidationErrorCode(RecordValidateRelationContextPolicies(object, map[string]any{}, "create")); got != "backend.policy.relation_context_required" {
		t.Fatalf("got %s", got)
	}
	if err := RecordValidateRelationContextPolicies(object, map[string]any{"account": "a"}, "create"); err != nil {
		t.Fatal(err)
	}
	object.Validations[0].Fields = nil
	object.Validations[0].Config = map[string]any{"fields": []any{"customer"}, "operations": []string{"update"}}
	if err := RecordValidateRelationContextPolicies(object, map[string]any{}, "create"); err != nil {
		t.Fatal(err)
	}
	field, ok := RecordRelationField(object, "customer")
	if !ok || RecordRelationTarget(field) != "customer" {
		t.Fatalf("%#v %v", field, ok)
	}
	if _, ok := RecordRelationField(object, "text"); ok {
		t.Fatal("text relation")
	}
	if RecordRelationTarget(definitionmodel.FieldSchema{Config: map[string]any{"target": "fallback"}}) != "fallback" || RecordRelationTarget(definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Target: " validation "}}) != "validation" {
		t.Fatal("relation target")
	}
	if !RecordValidationBlocks(policy) || !RecordPolicyAppliesToOperation(policy, "create") || RecordConfigString(" x ") != "x" || !RecordContainsText([]string{"x"}, "x") {
		t.Fatal("context wrappers")
	}

	fields := RecordDuplicateIdentityFields(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "email"}, {Key: "custom", Unique: true}}})
	if len(fields) != 2 {
		t.Fatalf("%#v", fields)
	}
	fields = RecordDuplicateIdentityFields(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}}})
	if len(fields) != 1 {
		t.Fatalf("%#v", fields)
	}
	if !RecordPolicyAllowsMissingRelatedLookup(definitionmodel.ValidationSchema{Config: map[string]any{"optional": true}}) || RecordPolicyAllowsMissingRelatedLookup(definitionmodel.ValidationSchema{}) {
		t.Fatal("missing lookup")
	}
	if !RecordPolicyValuesEqual("1", 1.0) || RecordPolicyValuesEqual(1, 2) || !RecordPolicyValuesEqual(" x ", "x") {
		t.Fatal("value equality")
	}
	validation := definitionmodel.ValidationSchema{FieldKey: "fallback", Fields: []string{"last"}, Config: map[string]any{"amount_field": "amount", "related_field": "limit"}}
	if RecordRelatedNumericValueField(validation) != "amount" || RecordRelatedNumericTargetField(validation) != "limit" {
		t.Fatal("numeric fields")
	}
	if RecordRelatedNumericLimitValueInScope(definitionmodel.ValidationSchema{Config: map[string]any{"gt": 1}}, 1) || RecordRelatedNumericLimitValueInScope(definitionmodel.ValidationSchema{Config: map[string]any{"gte": 2}}, 1) || !RecordRelatedNumericLimitValueInScope(definitionmodel.ValidationSchema{}, 1) {
		t.Fatal("numeric scope")
	}
	for _, tc := range []struct {
		target, required float64
		operator         string
		want             bool
	}{{2, 1, "gt", true}, {1, 1, "<=", true}, {1, 2, "<", true}, {1, 1, "eq", true}, {2, 1, "gte", true}} {
		if got := RecordRelatedNumericLimitSatisfied(tc.target, tc.required, tc.operator); got != tc.want {
			t.Fatalf("%#v", tc)
		}
	}
}

func TestRecordStateMachineValidationEdges(t *testing.T) {
	transition := map[string]any{"from": []string{"draft"}, "to": "approved", "required_fields": []string{"note"}, "required_permission": "order.approve", "effects": []any{map[string]any{"type": "set_fields", "field": "approved", "value": true}}}
	validation := definitionmodel.ValidationSchema{Key: "state", Type: "state_machine", FieldKey: "status", Config: map[string]any{"transitions": []any{transition}}}
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}
	if err := RecordValidateStateMachinePolicies(object, nil, nil, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if got := recordValidationErrorCode(RecordValidateStateMachinePolicies(object, map[string]any{"status": "draft"}, map[string]any{"status": "rejected"}, principalmodel.Principal{})); got != "backend.transition.invalid_transition" {
		t.Fatalf("got %s", got)
	}
	if got := recordValidationErrorCode(RecordValidateStateMachinePolicies(object, map[string]any{"status": "draft"}, map[string]any{"status": "approved"}, principalmodel.Principal{})); got != "backend.transition.required_field" {
		t.Fatalf("got %s", got)
	}
	if got := recordValidationErrorCode(RecordValidateStateMachinePolicies(object, map[string]any{"status": "draft"}, map[string]any{"status": "approved", "note": "ok"}, principalmodel.Principal{})); got != "backend.transition.permission_required" {
		t.Fatalf("got %s", got)
	}
	permissions := []string{"order.approve"}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	if err := RecordValidateStateMachinePolicies(object, map[string]any{"status": "draft"}, map[string]any{"status": "approved", "note": "ok"}, principal); err != nil {
		t.Fatal(err)
	}
	bad := transition
	bad["required_permission"] = "invalid"
	if got := recordValidationErrorCode(validateStructuredTransition(bad, map[string]any{"note": "ok"}, principal)); got != "backend.transition.invalid_permission" {
		t.Fatalf("got %s", got)
	}
	if effects := RecordTransitionEffects(transition); len(effects) != 1 {
		t.Fatalf("%#v", effects)
	}
	if effects := RecordTransitionEffects(map[string]any{"effect": map[string]any{"type": "x"}}); len(effects) != 1 {
		t.Fatalf("%#v", effects)
	}
	if RecordTransitionEffects(nil) != nil || RecordTransitionEffects(map[string]any{}) != nil {
		t.Fatal("empty effects")
	}
	if _, ok := RecordStateMachineTransition(validation, "missing", "approved"); ok {
		t.Fatal("unexpected transition")
	}
	if !transitionEndpointMatches("*", "anything") || transitionEndpointMatches("draft", "other") {
		t.Fatal("endpoint")
	}
	if RecordMessageCode("custom.code", "fallback") != "custom.code" || RecordMessageCode("plain text", "fallback") != "fallback" {
		t.Fatal("message")
	}
	if err := stateMachineError(apperror.KindBadRequest, "x", " ", "ignored"); recordValidationErrorCode(err) != "x" {
		t.Fatal(err)
	}
}

func TestRecordStateMachineEffectContractRequiresBusinessActionForExternalEffects(t *testing.T) {
	for _, effect := range []any{
		"audit:approved",
		map[string]any{"type": "create_record", "target_object": "activity"},
		map[string]any{"type": "update_related", "relation": "customer"},
		map[string]any{"type": "trigger_workflow", "workflow_key": "order.follow_up"},
	} {
		validation := definitionmodel.ValidationSchema{Type: "state_machine", Config: map[string]any{
			"transitions": []any{map[string]any{"from": "draft", "to": "approved", "effects": []any{effect}}},
		}}
		if got := recordValidationErrorCode(RecordValidateStateMachineEffectContract(validation)); got != "backend.transition.effect_requires_action" {
			t.Fatalf("effect=%#v code=%q", effect, got)
		}
	}
	for _, effectType := range []string{"patch_self", "update_self", "set_fields"} {
		validation := definitionmodel.ValidationSchema{Type: "state_machine", Config: map[string]any{
			"transitions": []any{
				map[string]any{"from": "draft", "to": "approved", "effects": []any{map[string]any{"type": effectType}}},
			},
		}}
		if err := RecordValidateStateMachineEffectContract(validation); err != nil {
			t.Fatalf("self effect %q rejected: %v", effectType, err)
		}
	}
	invalidPatch := definitionmodel.ValidationSchema{Type: "state_machine", Config: map[string]any{
		"transitions": []any{map[string]any{"from": "draft", "to": "approved", "self_patch": "reviewed=true"}},
	}}
	if got := recordValidationErrorCode(RecordValidateStateMachineEffectContract(invalidPatch)); got != "backend.transition.self_patch_invalid" {
		t.Fatalf("invalid patch code=%q", got)
	}
}

func TestRecordObjectRuleRemainingStatements(t *testing.T) {
	fields := []definitionmodel.FieldSchema{{Key: "a", Type: "text"}, {Key: "b", Type: "text"}, {Key: "n", Type: "number"}, {Key: "tags", Type: "json", Config: map[string]any{"options": []string{"x"}}}}
	tests := []struct {
		name       string
		validation definitionmodel.ValidationSchema
		data, prev map[string]any
	}{
		{"condition false", definitionmodel.ValidationSchema{Type: "required_fields", Fields: []string{"a"}, Config: map[string]any{"when": map[string]any{"field": "b", "equals": "yes"}}}, map[string]any{"b": "no"}, nil},
		{"required warning", definitionmodel.ValidationSchema{Type: "required_fields", Severity: "warning", Fields: []string{"a"}}, nil, nil},
		{"conditional warning", definitionmodel.ValidationSchema{Type: "conditional_required", Severity: "info"}, nil, nil},
		{"conditional config fields", definitionmodel.ValidationSchema{Type: "conditional_required", Config: map[string]any{"when_field": "a", "values": []string{"yes"}}, Fields: []string{"b"}}, map[string]any{"a": "no"}, nil},
		{"conditional value", definitionmodel.ValidationSchema{Type: "conditional_required", Config: map[string]any{"when_field": "a", "value": "yes"}, Fields: []string{"b"}}, map[string]any{"a": "no"}, nil},
		{"conditional too few", definitionmodel.ValidationSchema{Type: "conditional_required", Fields: []string{"a"}}, nil, nil},
		{"conditional blank", definitionmodel.ValidationSchema{Type: "conditional_required", Fields: []string{"", "b"}}, map[string]any{"a": "delivery"}, nil},
		{"required when warning", definitionmodel.ValidationSchema{Type: "required_when", Severity: "warning"}, nil, nil},
		{"required when empty field", definitionmodel.ValidationSchema{Type: "required_when"}, nil, nil},
		{"cross warning", definitionmodel.ValidationSchema{Type: "cross_field", Severity: "warning"}, nil, nil},
		{"not equal warning", definitionmodel.ValidationSchema{Type: "not_equal", Severity: "warning"}, nil, nil},
		{"not equal too few", definitionmodel.ValidationSchema{Type: "not_equal", Fields: []string{"a"}}, nil, nil},
		{"numeric warning", definitionmodel.ValidationSchema{Type: "numeric_min", Severity: "warning"}, nil, nil},
		{"range warning", definitionmodel.ValidationSchema{Type: "range", Severity: "warning"}, nil, nil},
		{"range empty field", definitionmodel.ValidationSchema{Type: "range"}, nil, nil},
		{"truthy warning", definitionmodel.ValidationSchema{Type: "truthy", Severity: "warning"}, nil, nil},
		{"enum warning", definitionmodel.ValidationSchema{Type: "enum_array", Severity: "warning"}, nil, nil},
		{"enum empty field", definitionmodel.ValidationSchema{Type: "enum_array"}, nil, nil},
		{"enum config options", definitionmodel.ValidationSchema{Type: "enum_array", FieldKey: "tags"}, map[string]any{"tags": []string{"x"}}, nil},
		{"hours warning", definitionmodel.ValidationSchema{Type: "business_hours_segments", Severity: "warning"}, nil, nil},
		{"hours field fallback", definitionmodel.ValidationSchema{Type: "business_hours_segments", Fields: []string{"a"}, Message: "custom.code"}, map[string]any{"a": "bad"}, nil},
		{"special warning", definitionmodel.ValidationSchema{Type: "special_dates_schedule", Severity: "warning"}, nil, nil},
		{"special fallback", definitionmodel.ValidationSchema{Type: "special_dates_schedule", Fields: []string{"a"}, Message: "custom.code"}, map[string]any{"a": "bad"}, nil},
		{"transition warning", definitionmodel.ValidationSchema{Type: "state_transition", Severity: "warning"}, nil, nil},
		{"transition empty field", definitionmodel.ValidationSchema{Type: "state_transition"}, nil, nil},
		{"relation warning", definitionmodel.ValidationSchema{Type: "relation_exists", Severity: "warning"}, nil, nil},
		{"unique warning", definitionmodel.ValidationSchema{Type: "composite_unique", Severity: "warning"}, nil, nil},
		{"state machine empty", definitionmodel.ValidationSchema{Type: "state_machine"}, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = validateObjectRules(definitionmodel.ObjectSchema{Fields: fields, Validations: []definitionmodel.ValidationSchema{tc.validation}}, tc.data, tc.prev)
		})
	}
	if _, err := RecordNormalizeData(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "n", Type: "number"}}}, map[string]any{"n": "bad"}, false); err == nil {
		t.Fatal("normalize invalid number")
	}
	if stringListAny(" ") != nil {
		t.Fatal("blank string list")
	}
	if err := validateSpecialDatesScheduleValue("bad"); err == nil {
		t.Fatal("special JSON")
	}
	if err := validateBusinessHourSegmentsValue("bad"); err == nil {
		t.Fatal("hours JSON")
	}
	if _, err := jsonArrayAny(" "); err != nil {
		t.Fatal(err)
	}
	if isI18nErrorCode("bad@code.x") {
		t.Fatal("invalid i18n code")
	}
}

func TestRecordPolicyHelperRemainingStatements(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"order.approve"}})
	if err := RecordValidateImmutableAfterStatusPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}}}, map[string]any{}, map[string]any{}, "update", principal); err != nil {
		t.Fatal(err)
	}
	if err := RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, {Type: "threshold_permission", Severity: "warning"}}}, nil, nil, "update", principal); err != nil {
		t.Fatal(err)
	}
	threshold := definitionmodel.ValidationSchema{Type: "threshold_permission", Config: map[string]any{"field": "amount", "threshold": 100, "permission": "order.approve", "changed_only": true}}
	if err := RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{threshold}}, map[string]any{"amount": "101"}, map[string]any{"amount": 101}, "update", principal); err != nil {
		t.Fatal(err)
	}
	threshold.Config["field"] = ""
	threshold.Fields = nil
	if err := RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{threshold}}, nil, map[string]any{}, "update", principal); err != nil {
		t.Fatal(err)
	}
	contextPolicy := definitionmodel.ValidationSchema{Type: "relation_context", Config: map[string]any{"fields": []string{"customer"}}}
	if err := RecordValidateRelationContextPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{contextPolicy}}, map[string]any{"customer": "c1"}, "update"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		validation definitionmodel.ValidationSchema
		want       string
	}{
		{definitionmodel.ValidationSchema{Config: map[string]any{"required_field": "r"}}, "r"},
		{definitionmodel.ValidationSchema{Config: map[string]any{"value_field": "v"}}, "v"},
		{definitionmodel.ValidationSchema{FieldKey: "f"}, "f"},
		{definitionmodel.ValidationSchema{Fields: []string{"list"}}, "list"},
		{definitionmodel.ValidationSchema{}, ""},
	} {
		if got := RecordRelatedNumericValueField(tc.validation); got != tc.want {
			t.Fatalf("%#v=%s", tc.validation, got)
		}
	}
	for _, tc := range []struct {
		config map[string]any
		want   string
	}{{map[string]any{"target_field": "t"}, "t"}, {map[string]any{"numeric_field": "n"}, "n"}, {map[string]any{}, ""}} {
		if got := RecordRelatedNumericTargetField(definitionmodel.ValidationSchema{Config: tc.config}); got != tc.want {
			t.Fatalf("%#v=%s", tc.config, got)
		}
	}

	if err := RecordValidateStateMachinePolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, {Type: "state_machine", Severity: "warning"}, {Type: "state_machine"}}}, map[string]any{}, map[string]any{}, principal); err != nil {
		t.Fatal(err)
	}
	state := definitionmodel.ValidationSchema{Type: "state_machine", FieldKey: "status", Config: map[string]any{"transitions": []any{map[string]any{"from": "draft", "to": "done"}}}}
	if err := RecordValidateStateMachinePolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{state}}, map[string]any{"status": "draft"}, map[string]any{"status": "draft"}, principal); err != nil {
		t.Fatal(err)
	}
	if effects := RecordTransitionEffects(map[string]any{"effects": []any{"x"}}); len(effects) != 1 {
		t.Fatalf("%#v", effects)
	}
	if effects := RecordTransitionEffects(map[string]any{"effects": "bad"}); effects != nil {
		t.Fatalf("%#v", effects)
	}
	if err := validateStructuredTransition(map[string]any{}, nil, principal); err != nil {
		t.Fatal(err)
	}
	if got := transitionRequiredFields(map[string]any{"require_fields": []string{"x"}}); len(got) != 1 {
		t.Fatalf("%#v", got)
	}
	if got := transitionRequiredPermission(map[string]any{"require_permission": "order.approve"}); got != "order.approve" {
		t.Fatalf("%s", got)
	}
}

func TestRecordObjectRuleFallbackStatements(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "a", Type: "text"}, {Key: "b", Type: "text"}}}
	for _, config := range []map[string]any{
		{"when_field": "a", "values": []string{"yes"}},
		{"when_field": "a", "value": "yes"},
	} {
		object.Validations = []definitionmodel.ValidationSchema{{Type: "conditional_required", Fields: []string{"b"}, Config: config}}
		if err := validateObjectRules(object, map[string]any{"a": "yes", "b": "present"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, validationType := range []string{"business_hours_segments", "special_dates_schedule"} {
		object.Validations = []definitionmodel.ValidationSchema{{Type: validationType}}
		if err := validateObjectRules(object, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	object.Validations = []definitionmodel.ValidationSchema{{Type: "special_dates_schedule", FieldKey: "a"}}
	if err := validateObjectRules(object, map[string]any{"a": "bad"}, nil); err == nil {
		t.Fatal("special schedule error")
	}
	if _, err := jsonArrayAny(""); err != nil {
		t.Fatal(err)
	}
}
