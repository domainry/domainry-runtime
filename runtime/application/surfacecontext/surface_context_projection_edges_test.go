package surfacecontext

import (
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	"reflect"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"
)

func TestSurfaceContextDisplayProjectionFallbacks(t *testing.T) {
	object := definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": "preferred"}}}
	for _, test := range []struct {
		name   string
		object definitionmodel.ObjectSchema
		record recordmodel.Record
		key    string
		value  string
	}{
		{name: "non map display", object: definitionmodel.ObjectSchema{UX: map[string]any{"display": "name"}}, record: recordmodel.Record{Data: map[string]any{"name": "Name"}}, key: "name", value: "Name"},
		{name: "blank title field", object: definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": ""}}}, record: recordmodel.Record{Data: map[string]any{"name": "Name"}}, key: "name", value: "Name"},
		{name: "nil title field", object: definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": nil}}}, record: recordmodel.Record{Data: map[string]any{"name": "Name"}}, key: "name", value: "Name"},
		{name: "blank preferred value", object: object, record: recordmodel.Record{Data: map[string]any{"preferred": "", "name": "Name"}}, key: "name", value: "Name"},
		{name: "blank conventional value", object: definitionmodel.ObjectSchema{}, record: recordmodel.Record{Data: map[string]any{"name": "", "title": "Title"}}, key: "title", value: "Title"},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, value := SurfaceContextRecordDisplay(test.object, test.record)
			if key != test.key || value != test.value {
				t.Fatalf("key=%q value=%q", key, value)
			}
		})
	}
	for _, test := range []struct {
		name   string
		record recordmodel.Record
		fields []string
		key    string
		value  string
	}{
		{"conventional field", recordmodel.Record{ID: "r1", Data: map[string]any{"name": "Name"}}, nil, "name", "Name"},
		{"record id", recordmodel.Record{ID: "r2", Data: map[string]any{}}, nil, "id", "r2"},
		{"permitted field", recordmodel.Record{ID: "r3", Data: map[string]any{"code": "C-3"}}, []string{" ", "code"}, "code", "C-3"},
		{"permitted fallback id", recordmodel.Record{ID: "r4", Data: map[string]any{}}, []string{"missing"}, "id", "r4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var key, value string
			if test.fields == nil {
				key, value = SurfaceContextRecordDisplay(object, test.record)
			} else {
				key, value = SurfaceContextPermittedRecordDisplay(object, test.record, test.fields)
			}
			if key != test.key || value != test.value {
				t.Fatalf("key=%q value=%q", key, value)
			}
		})
	}
	key, value := SurfaceContextPermittedRecordDisplay(object, recordmodel.Record{ID: "r5", Data: map[string]any{"title": "Title"}}, nil)
	if key != "title" || value != "Title" {
		t.Fatalf("empty permission fields fallback key=%q value=%q", key, value)
	}
	key, value = SurfaceContextPermittedRecordDisplay(object, recordmodel.Record{ID: "r6", Data: map[string]any{" name ": "Raw Name"}}, []string{" name "})
	if key != " name " || value != "Raw Name" {
		t.Fatalf("raw permission field key=%q value=%q", key, value)
	}
	key, value = SurfaceContextPermittedRecordDisplay(object, recordmodel.Record{ID: "r7", Data: map[string]any{"name": "", "code": "C-7"}}, []string{"name", "code"})
	if key != "code" || value != "C-7" {
		t.Fatalf("blank first permission field key=%q value=%q", key, value)
	}
	key, value = SurfaceContextPermittedRecordDisplay(object, recordmodel.Record{ID: "r8", Data: map[string]any{"name": ""}}, []string{"name"})
	if key != "id" || value != "r8" {
		t.Fatalf("blank permission fallback key=%q value=%q", key, value)
	}
}

func TestSurfaceContextReferencePermissionAndDisplayFieldDeduplication(t *testing.T) {
	role := accessfixture.Bundle{ReferencePolicies: []accessfixture.ReferencePolicyFixture{
		{SourceObjectKey: "other", RelationFieldKey: "owner", TargetObjectKey: "user", Mode: "label_only"},
		{SourceObjectKey: "order", RelationFieldKey: "owner", TargetObjectKey: "account", Mode: "label_only"},
		{SourceObjectKey: "order", RelationFieldKey: "customer", TargetObjectKey: "contact", Mode: "label_only"},
		{SourceObjectKey: "order", RelationFieldKey: "customer", TargetObjectKey: "account", Mode: "readable"},
		{SourceObjectKey: " order ", RelationFieldKey: " customer ", TargetObjectKey: " account ", Mode: ""},
		{SourceObjectKey: "order", RelationFieldKey: "customer", TargetObjectKey: "account", Mode: "readable"},
	}}
	permission, ok := SurfaceContextReferencePermission(accessfixture.Attach(principalmodel.Principal{}, role), "order", "customer", "account")
	if !ok || permission.SourceResource != "order" {
		t.Fatalf("permission=%#v ok=%v", permission, ok)
	}
	if _, ok := SurfaceContextReferencePermission(accessfixture.Attach(principalmodel.Principal{}, role), "missing", "customer", "account"); ok {
		t.Fatal("unmatched reference permission accepted")
	}
	fields := SurfaceContextAppendReferenceDisplayFields([]string{" title ", ""}, []string{"title", " code ", ""})
	if !reflect.DeepEqual(fields, []string{" title ", "", "code"}) {
		t.Fatalf("fields=%#v", fields)
	}
	unionRole := accessfixture.Bundle{ReferencePolicies: []accessfixture.ReferencePolicyFixture{
		{SourceObjectKey: "order", RelationFieldKey: "customer", TargetObjectKey: "account", DisplayFields: []string{"name"}, Reason: "allowed"},
		{SourceObjectKey: "order", RelationFieldKey: "customer", TargetObjectKey: "account", DisplayFields: []string{"code", "name"}},
	}}
	union, ok := SurfaceContextReferencePermission(accessfixture.Attach(principalmodel.Principal{}, unionRole), "order", "customer", "account")
	if !ok || !reflect.DeepEqual(union.DisplayFields, []string{"name", "code"}) {
		t.Fatalf("reference union=%#v ok=%v", union, ok)
	}
	unionRole.Guardrails = []accessfixture.GuardrailFixture{{FieldRestrictions: []accessfixture.FieldRestrictionFixture{{
		ObjectKey: "order", FieldKey: "customer", Actions: []string{"read"},
	}}}}
	if _, ok := SurfaceContextReferencePermission(accessfixture.Attach(principalmodel.Principal{}, unionRole), "order", "customer", "account"); ok {
		t.Fatal("reference label projection bypassed field guardrail")
	}
}

func TestSurfaceContextMetricsMaskingReportsAndSortingHelpers(t *testing.T) {
	objects := map[string]surfacecontextmodel.SurfaceContextObjectResult{"order": {Page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "1"}, {ID: "2"}}, Total: 3, HasNext: true}}}
	metrics := SurfaceContextMetrics(objects)["objects"].(map[string]any)["order"].(map[string]any)
	if metrics["count"] != 2 || metrics["total"] != 3 || metrics["has_next"] != true {
		t.Fatalf("metrics=%#v", metrics)
	}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "order", FieldKey: "secret", Read: true, Masked: true}}})
	masked := SurfaceContextMaskedFields(principal, definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "visible"}, {Key: "secret"}}})
	if !reflect.DeepEqual(masked, []string{"secret"}) {
		t.Fatalf("masked=%#v", masked)
	}
	if count := SurfaceContextRelationLabelCount(map[string]map[string]string{"account": {"a": "A", "b": "B"}, "user": {"u": "U"}}); count != 3 {
		t.Fatalf("count=%d", count)
	}
	sources := SurfaceContextReportSourceObjects(reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "customer", Alias: "customer"}}}})
	if !reflect.DeepEqual(sources, []string{"order", "customer"}) {
		t.Fatalf("sources=%#v", sources)
	}
	sources = SurfaceContextReportSourceObjects(reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: " order "}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "order"}, {ObjectKey: " "}}}})
	if !reflect.DeepEqual(sources, []string{"order"}) {
		t.Fatalf("deduplicated sources=%#v", sources)
	}
	if ids := SurfaceContextSortedIDs(map[string]bool{"b": true, "a": true}); !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("ids=%#v", ids)
	}
	if SurfaceContextValueOrDefault(" value ", "fallback") != "value" || SurfaceContextValueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
}
