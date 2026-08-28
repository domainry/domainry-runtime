package validation

import (
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	"math"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordAccessConditionOutcomes(t *testing.T) {
	for _, validationType := range []string{"immutable_after_status", "immutable_when_status", "locked_fields"} {
		validation := definitionmodel.ValidationSchema{Type: validationType, Fields: []string{"", "amount"}, Severity: "warning", Config: map[string]any{"statuses": []string{"closed"}}}
		if err := RecordValidateImmutableAfterStatusPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, map[string]any{"status": "closed", "amount": 1}, map[string]any{"status": "closed", "amount": 1}, "update", principalmodel.Principal{}); err != nil {
			t.Fatal(err)
		}
		validation.Severity = ""
		validation.Config["when_field"] = "kind"
		validation.Config["value"] = "special"
		if err := RecordValidateImmutableAfterStatusPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, map[string]any{"status": "closed"}, map[string]any{"kind": "ordinary"}, "update", principalmodel.Principal{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, validationType := range []string{"threshold_permission", "permission_threshold"} {
		validation := definitionmodel.ValidationSchema{Type: validationType, FieldKey: "amount", Severity: "warning", Config: map[string]any{"threshold": 100, "permission": "order.approve"}}
		object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}
		if err := RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 1}, "update", principalmodel.Principal{}); err != nil {
			t.Fatal(err)
		}
		validation.Severity = ""
		validation.Config["operations"] = []string{"create"}
		object.Validations[0] = validation
		if err := RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 101}, "update", principalmodel.Principal{}); err != nil {
			t.Fatal(err)
		}
		validation.Config = map[string]any{"threshold": 100, "permission": "order.approve", "when_field": "kind", "value": "special"}
		object.Validations[0] = validation
		if err := RecordValidateThresholdPermissionPolicies(object, nil, map[string]any{"amount": 101, "kind": "ordinary"}, "update", principalmodel.Principal{}); err != nil {
			t.Fatal(err)
		}
	}
	validation := definitionmodel.ValidationSchema{Type: "threshold_permission", FieldKey: "amount", Config: map[string]any{"threshold": 100, "permission": "order.approve", "changed_only": true}}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"order.approve"}})
	if err := RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, nil, map[string]any{"amount": 1}, "update", principal); err != nil {
		t.Fatal(err)
	}
	if err := RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, map[string]any{"amount": 1}, map[string]any{"amount": 2}, "update", principal); err != nil {
		t.Fatal(err)
	}
}

func TestRecordNormalizeConditionOutcomes(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), 1} {
		_ = validateFieldType(definitionmodel.FieldSchema{Key: "n", Type: "number"}, value)
	}
	_ = validateFieldType(definitionmodel.FieldSchema{Key: "flag", Type: "boolean"}, true)
	for _, field := range []definitionmodel.FieldSchema{
		{Key: "text", Type: "text", Config: map[string]any{"pattern": "["}},
		{Key: "text", Type: "text", Config: map[string]any{"pattern": "^x$"}},
		{Key: "select", Type: "select"},
		{Key: "select", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"x"}}},
		{Key: "number", Type: "number", Config: map[string]any{"min": 0, "max": 10}},
	} {
		_ = validateFieldRules(field, map[string]any{"text": "y", "select": "x", "number": 5}[field.Key])
	}
	for _, field := range []definitionmodel.FieldSchema{
		{Config: map[string]any{"options": []string{"x", ""}}},
		{Config: map[string]any{"options": []any{"x", ""}}},
		{Options: []any{"x", ""}},
		{Options: map[string]any{"x": "X", "": "blank"}},
	} {
		_ = selectFieldOptions(field)
	}
	if got := compactStrings([]string{"", " x "}); len(got) != 1 {
		t.Fatalf("%#v", got)
	}
}

func TestRecordQueryConditionOutcomes(t *testing.T) {
	object, view := recordQueryFixture()
	_ = RecordSelectListView([]definitionmodel.ViewSchema{view}, "order", "missing")
	query := recordmodel.RecordListQuery{Page: 2, PageSize: 10, SearchFields: []string{"name"}, Sort: []recordmodel.RecordSortRule{{Field: "name"}}, Filters: map[string]any{"name": nil, "id__in": []any{}, "amount__lte": "10", "fixed": true}}
	_ = RecordNormalizeListQuery(object, view, query, principalmodel.Principal{})
	badView := definitionmodel.ViewSchema{Config: map[string]any{"filters": []any{map[string]any{"key": "bad", "field": "missing", "value": "x"}, map[string]any{"key": "broken", "field": "name", "value": func() {}}}}}
	_ = normalizeListFilters(object, badView, map[string]any{"bad": true, "broken": true}, principalmodel.Principal{})
	_ = normalizedListFilterValues(definitionmodel.FieldSchema{}, []any{"", nil, "x"}, true)
	_, _ = splitSortRule("name desc")
}

func TestRecordRelatedAndStateConditionOutcomes(t *testing.T) {
	fields := RecordDuplicateIdentityFields(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "phone"}, {Key: "title"}}})
	if len(fields) != 1 || fields[0].Key != "phone" {
		t.Fatalf("%#v", fields)
	}
	fields = RecordDuplicateIdentityFields(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "title"}}})
	if len(fields) != 1 {
		t.Fatalf("%#v", fields)
	}
	_ = RecordPolicyValuesEqual(1, "not-number")
	_ = RecordRelatedNumericLimitValueInScope(definitionmodel.ValidationSchema{Config: map[string]any{"gt": 1}}, 2)
	_ = RecordRelatedNumericLimitValueInScope(definitionmodel.ValidationSchema{Config: map[string]any{"gte": 1}}, 2)
	state := definitionmodel.ValidationSchema{Type: "state_machine", FieldKey: "status", Config: map[string]any{"transitions": []any{map[string]any{"from": "draft", "to": "done", "required_fields": []string{""}, "required_permission": "order."}}}}
	_ = RecordValidateStateMachinePolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{state}}, map[string]any{"status": ""}, map[string]any{"status": "done"}, principalmodel.Principal{})
	_ = RecordValidateStateMachinePolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{state}}, map[string]any{"status": "draft"}, map[string]any{"status": ""}, principalmodel.Principal{})
	_, _ = RecordStateMachineTransition(state, "draft", "done")
	_ = RecordMessageCode("code.with-dash", "fallback")
}

func TestRecordObjectRuleConditionOutcomes(t *testing.T) {
	fields := []definitionmodel.FieldSchema{{Key: "a", Type: "text"}, {Key: "b", Type: "text"}, {Key: "n", Type: "number"}, {Key: "tags", Type: "json", Validation: definitionmodel.FieldValidation{Options: []string{"x"}}}}
	run := func(validation definitionmodel.ValidationSchema, data, prev map[string]any) error {
		return validateObjectRules(definitionmodel.ObjectSchema{Fields: fields, Validations: []definitionmodel.ValidationSchema{validation}}, data, prev)
	}
	_ = RecordValidateDataWithPrev(definitionmodel.ObjectSchema{}, map[string]any{}, map[string]any{}, false)
	_ = run(definitionmodel.ValidationSchema{Type: "required_fields", Fields: []string{"", "a"}}, map[string]any{"a": "present"}, nil)
	for _, tc := range []struct {
		config map[string]any
		data   map[string]any
	}{
		{map[string]any{"when_field": "a", "required_fields": []string{"", "b"}}, map[string]any{"a": "x", "b": "present"}},
		{map[string]any{"when_field": "a", "values": []string{"yes"}}, map[string]any{"a": "no", "b": "present"}},
		{map[string]any{"when_field": "a", "value": "yes"}, map[string]any{"a": "yes", "b": "present"}},
	} {
		_ = run(definitionmodel.ValidationSchema{Type: "conditional_required", Fields: []string{"", "b"}, Config: tc.config}, tc.data, nil)
	}
	for _, data := range []map[string]any{{"a": "pickup"}, {"a": "delivery", "b": "present"}} {
		_ = run(definitionmodel.ValidationSchema{Type: "conditional_required", Fields: []string{"a", "b"}}, data, nil)
	}
	_ = run(definitionmodel.ValidationSchema{Type: "required_when", FieldKey: "a", Fields: []string{"", "b"}, Config: map[string]any{"value": "yes"}}, map[string]any{"a": "yes", "b": "present"}, nil)
	for _, data := range []map[string]any{{"a": "x", "b": 2}, {"a": 1, "b": "x"}, {"a": 2, "b": 1}} {
		_ = run(definitionmodel.ValidationSchema{Type: "cross_field", Fields: []string{"a", "b"}}, data, nil)
	}
	for _, values := range [][]string{{"", "b"}, {"a", ""}, {"a", "b"}} {
		_ = run(definitionmodel.ValidationSchema{Type: "not_equal", Fields: values}, map[string]any{"a": "x", "b": "y"}, nil)
	}
	_ = run(definitionmodel.ValidationSchema{Type: "not_equal", Fields: []string{"a", "b"}}, map[string]any{"a": "x", "b": ""}, nil)
	for _, validation := range []definitionmodel.ValidationSchema{{Type: "numeric_min", FieldKey: "n"}, {Type: "numeric_min", FieldKey: "n", Config: map[string]any{"min": 1}}} {
		_ = run(validation, map[string]any{"n": 2}, nil)
		_ = run(validation, map[string]any{"n": "bad"}, nil)
	}
	for _, tc := range []struct {
		config map[string]any
		data   map[string]any
	}{
		{map[string]any{"exclusive_min": 1}, map[string]any{"n": 2}},
		{map[string]any{"min": 1}, map[string]any{"n": 2}},
		{map[string]any{"min_field": "a"}, map[string]any{"n": 2, "a": 1}},
		{map[string]any{"min_field": "a"}, map[string]any{"n": 2, "a": "bad"}},
		{map[string]any{"min_field": "a"}, map[string]any{"n": "2026-02-02", "a": "bad"}},
		{map[string]any{"min_field": "a"}, map[string]any{"n": "2026-02-02", "a": "2026-01-01"}},
		{map[string]any{"max": 10}, map[string]any{"n": 2}},
		{map[string]any{"exclusive_max_field": "a"}, map[string]any{"n": 2, "a": 3}},
		{map[string]any{"exclusive_max_field": "a"}, map[string]any{"n": 2, "a": "bad"}},
		{map[string]any{"exclusive_max_field": "a"}, map[string]any{"n": "2026-01-01", "a": "bad"}},
		{map[string]any{"exclusive_max_field": "a"}, map[string]any{"n": "2026-01-01", "a": "2026-02-01"}},
		{map[string]any{"max_field": "a"}, map[string]any{"n": 2, "a": 3}},
		{map[string]any{"max_field": "a"}, map[string]any{"n": 2, "a": "bad"}},
		{map[string]any{"max_field": "a"}, map[string]any{"n": "2026-01-01", "a": "bad"}},
		{map[string]any{"max_field": "a"}, map[string]any{"n": "2026-01-01", "a": "2026-02-01"}},
	} {
		_ = run(definitionmodel.ValidationSchema{Type: "range", FieldKey: "n", Config: tc.config}, tc.data, nil)
	}
	_ = run(definitionmodel.ValidationSchema{Type: "truthy", FieldKey: "a"}, map[string]any{"a": true}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "enum_array", FieldKey: "tags", Config: map[string]any{"min_items": 1, "options": []string{"x"}}}, map[string]any{"tags": []string{"x"}}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "enum_array", FieldKey: "tags"}, map[string]any{"tags": []string{"x"}}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "state_transition", FieldKey: "a", Config: map[string]any{"allowed_from": []string{"x"}}}, map[string]any{"a": ""}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "state_transition", FieldKey: "a", Config: map[string]any{"allowed_from": []string{"x"}}}, map[string]any{"a": "x"}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "relation_exists", FieldKey: "a"}, map[string]any{"a": "present"}, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "capability_available", Config: map[string]any{"capability": "x"}}, nil, nil)
	_ = run(definitionmodel.ValidationSchema{Type: "state_machine", FieldKey: "a", Config: map[string]any{"state_field": "a", "transitions": []any{map[string]any{"from": "old", "to": "new"}}}}, map[string]any{"a": "same"}, map[string]any{"a": "same"})
	_ = run(definitionmodel.ValidationSchema{Type: "composite_unique", Fields: []string{"", "a"}}, map[string]any{"a": "present"}, nil)
}

func TestRecordNormalizationAndSegmentConditionOutcomes(t *testing.T) {
	_ = validateFieldRules(definitionmodel.FieldSchema{Key: "text", Type: "text", Config: map[string]any{"pattern": "^x$"}}, "x")
	_ = validateFieldRules(definitionmodel.FieldSchema{Key: "select", Type: "select"}, "x")
	_ = validateFieldRules(definitionmodel.FieldSchema{Key: "select", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"x"}}}, "x")
	_ = validateFieldRules(definitionmodel.FieldSchema{Key: "n", Type: "number"}, 1)
	for _, field := range []definitionmodel.FieldSchema{
		{Config: map[string]any{"value_domain": map[string]any{"items": []any{}}}},
		{Config: map[string]any{"valueDomain": map[string]any{"items": []any{}}}},
	} {
		_ = selectFieldOptions(field)
	}
	_ = dictionaryItemOptionKeys([]metadatamodel.DictionaryItemSchema{{}})
	_ = dictionaryItemOptionKeys([]map[string]any{{}})
	_ = dictionaryItemOptionKeys([]any{metadatamodel.DictionaryItemSchema{}, map[string]any{}, ""})
	if dictionaryItemOptionKey("", "") != "" {
		t.Fatal("empty option")
	}

	_ = validationConditionApplies(definitionmodel.ValidationSchema{Config: map[string]any{"when_field": "x"}}, map[string]any{"x": blankRecordStringer{}})
	_ = validationValueMatches(1, map[string]any{"nonzero": false})
	_ = validationValueMatches("x", map[string]any{"value": nil})
	_ = stringListAny([]any{"", "x"})
	_ = validateSpecialDatesScheduleValue([]any{map[string]any{"date": ""}})
	_ = validateSpecialDatesScheduleValue([]any{map[string]any{"date": "2026-01-01"}})
	_ = validateSpecialDatesScheduleValue([]any{map[string]any{"date": "2026-01-01", "segments": ""}})
	_ = validateSpecialDatesScheduleValue([]any{map[string]any{"date": "2026-01-01", "segments": []any{map[string]any{"opens_at": "09:00", "closes_at": "10:00"}}}})
	for _, segments := range []any{
		[]any{map[string]any{"opens_at": "", "closes_at": "10:00"}},
		[]any{map[string]any{"opens_at": "09:00", "closes_at": ""}},
		[]any{map[string]any{"opens_at": "09:00", "closes_at": nil}},
		[]any{map[string]any{"opens_at": "10:00", "closes_at": "12:00"}, map[string]any{"opens_at": "09:00", "closes_at": "10:00"}},
	} {
		_ = validateBusinessHourSegmentsValue(segments)
	}
	_ = stateMachineValue(map[string]any{"x": nil}, "x")
	_, _ = validationDateOnly(blankRecordStringer{})
}

func TestRecordTimePolicyAndUtilityConditionOutcomes(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	for _, validationType := range []string{"retention_guard", "retention_policy", "legal_hold"} {
		validation := definitionmodel.ValidationSchema{Type: validationType, Config: map[string]any{"operations": []string{"create"}}}
		_ = validateRetentionPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, nil, map[string]any{}, "update", now)
		validation.Config = map[string]any{"when_field": "kind", "value": "special"}
		_ = validateRetentionPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, nil, map[string]any{"kind": "ordinary"}, "delete", now)
	}
	_ = validateRetentionPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "retention_guard"}}}, nil, map[string]any{"legal_hold": false}, "delete", now)
	for _, validationType := range []string{"stale_record_guard", "stale_activity_guard"} {
		validation := definitionmodel.ValidationSchema{Type: validationType, Config: map[string]any{"operations": []string{"create"}}}
		_ = validateStaleRecordPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, map[string]any{}, "update", now)
		validation.Config = map[string]any{"when_field": "kind", "value": "special"}
		_ = validateStaleRecordPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{validation}}, map[string]any{"kind": "ordinary"}, "update", now)
	}
	_ = validateStaleRecordPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "stale_record_guard"}}}, map[string]any{}, "update", now)
	_ = validateStaleRecordPoliciesAt(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "stale_record_guard"}}}, map[string]any{"last_activity_at": "2020-01-01"}, "update", now)
	for _, code := range []string{"A.code", "1.code", "-.code"} {
		_ = RecordPolicyMessageCode(code, "fallback")
	}
	_ = RecordRetentionPolicyDeleteIntent(definitionmodel.ValidationSchema{}, nil, map[string]any{"status": ""}, "update")
	_, _, _, _ = RecordTimeOverlapRange(map[string]any{"start": "2026-01-01", "end": ""}, "start", "end")
	_, _, _, _ = RecordTimeOverlapRange(map[string]any{"start": "2026-01-01", "end": "2026-01-02T00:00:00Z"}, "start", "end")
	_ = timeOverlapResourceGroups(definitionmodel.ValidationSchema{Config: map[string]any{"scope_fields": []string{"", "owner"}}})
	_ = timeOverlapResourceGroupMatches([]string{"owner"}, map[string]any{"owner": "x"}, map[string]any{"owner": ""})
	_ = stringGroupsFromAny([]any{[]string{"", "owner"}})
	_, _ = RecordDateOnly(blankRecordStringer{})
	_ = RecordPolicyValueOnOrAfter("2026-01-01", "bad")
	_ = RecordPolicyValueOnOrBefore("2026-01-01", "bad")
	_ = RecordThresholdPermissionExceeded(definitionmodel.ValidationSchema{Config: map[string]any{"ratio_gt": 2, "ratio_field": "base"}}, 10, map[string]any{"base": "bad"})
}

func TestRecordImportContextAndErrorConditionOutcomes(t *testing.T) {
	_ = RecordImportFieldHeaderAliases(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "", Name: "", Config: map[string]any{"label": ""}}}})
	_, _ = RecordCoerceImportValue("o", definitionmodel.FieldSchema{Type: "percent"}, "12.5")
	_ = importValueDomainAliases(metadatamodel.DictionaryItemSchema{Config: map[string]any{"alias": ""}})
	_ = importStringListFromAny("")
	field := definitionmodel.FieldSchema{Key: "status", Type: "select", Config: map[string]any{"options": []string{"open"}}}
	_, _ = importValueDomainValue("order", field, "not-localized")
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, {Type: "relation_context", Severity: "warning"}, {Type: "relation_context", Fields: []string{"", "customer"}}}}
	_ = RecordValidateRelationContextPolicies(object, map[string]any{"customer": "x"}, "update")
	for _, code := range []string{"A.code", "1.code", "-.code", "{.code", "bad code.x"} {
		_ = isI18nErrorCode(code)
	}
}

func TestRecordAccessStateAndQueryRemainingConditions(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"order.approve"}})
	for _, lock := range []definitionmodel.ValidationSchema{
		{Type: "locked_fields"},
		{Type: "locked_fields", Config: map[string]any{"statuses": []string{"closed"}}},
		{Type: "locked_fields", Fields: []string{"", "amount"}, Config: map[string]any{"statuses": []string{"closed"}}},
	} {
		_ = RecordValidateImmutableAfterStatusPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{lock}}, map[string]any{"status": "closed", "amount": 1}, map[string]any{"status": "closed", "amount": 1}, "update", principal)
	}
	_ = RecordValidateThresholdPermissionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "threshold_permission", FieldKey: "amount", Config: map[string]any{"threshold": 1, "permission": "order.approve"}}}}, nil, map[string]any{"amount": ""}, "update", principal)
	_ = validateStructuredTransition(map[string]any{"required_fields": []string{""}, "required_permission": "order."}, map[string]any{}, principalmodel.Principal{})
	_ = RecordMessageCode("code.with space", "fallback")
	object, _ := recordQueryFixture()
	_ = RecordSelectListView([]definitionmodel.ViewSchema{{Key: "x", ObjectKey: "order", Config: map[string]any{"business_view": "detail"}}}, "order", "")
	_ = normalizeListFilters(object, definitionmodel.ViewSchema{Config: map[string]any{}}, map[string]any{"amount__gte": "1", "unresolved": true}, principalmodel.Principal{})
	_, _ = splitSortRule("name")
}

func TestRecordFinalConditionOutcomes(t *testing.T) {
	_ = policyConditionFieldMatches("x", nil, nil, nil, map[string]any{"x": blankRecordStringer{}})
	_ = RecordThresholdPermissionExceeded(definitionmodel.ValidationSchema{Config: map[string]any{"ratio_gt": 2, "ratio_field": "base"}}, 10, map[string]any{"base": nil})
	_ = RecordThresholdPermissionAllExceeded(definitionmodel.ValidationSchema{Config: map[string]any{"ratio_gt": 2, "ratio_field": "base"}}, 10, map[string]any{"base": nil})
	_ = RecordDuplicateIdentityFields(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "custom"}}})
	_ = RecordPolicyMessageCode("{.code", "fallback")
	fields := []definitionmodel.FieldSchema{{Key: "a", Type: "text"}, {Key: "tags", Type: "json"}}
	run := func(validation definitionmodel.ValidationSchema, data, prev map[string]any) {
		_ = validateObjectRules(definitionmodel.ObjectSchema{Fields: fields, Validations: []definitionmodel.ValidationSchema{validation}}, data, prev)
	}
	run(definitionmodel.ValidationSchema{Type: "conditional_required", Fields: []string{"a", ""}}, map[string]any{"a": "delivery"}, nil)
	run(definitionmodel.ValidationSchema{Type: "truthy", FieldKey: "a"}, map[string]any{"a": "bad"}, nil)
	run(definitionmodel.ValidationSchema{Type: "enum_array", FieldKey: "tags"}, map[string]any{"tags": []string{}}, nil)
	state := definitionmodel.ValidationSchema{Type: "state_machine", FieldKey: "a", Config: map[string]any{"state_field": "a", "transitions": []any{}}}
	run(state, map[string]any{"a": ""}, map[string]any{"a": "old"})
	run(state, map[string]any{"a": "new"}, map[string]any{"a": ""})
}
