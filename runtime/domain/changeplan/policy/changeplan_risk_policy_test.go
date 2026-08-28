package policy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func changePlanRaw(value string) json.RawMessage { return json.RawMessage(value) }

func TestChangePlanExpectedChangeKindMatrix(t *testing.T) {
	typeBefore, typeAfter := changePlanRaw(`{"type":"text"}`), changePlanRaw(`{"type":"number"}`)
	requiredBefore, requiredAfter := changePlanRaw(`{"required":false}`), changePlanRaw(`{"required":true}`)
	optionsBefore, optionsAfter := changePlanRaw(`{"options":["a","b"]}`), changePlanRaw(`{"options":["a"]}`)
	for _, test := range []struct {
		operation, resourceType string
		before, after           json.RawMessage
		want                    string
	}{{"create", "field", nil, nil, "additive"}, {"archive", "field", nil, nil, "destructive"}, {"delete", "field", nil, nil, "destructive"}, {"update", "field", typeBefore, typeAfter, "destructive"}, {"update", "field", requiredBefore, requiredAfter, "breaking"}, {"update", "field", optionsBefore, optionsAfter, "breaking"}, {"update", "field", changePlanRaw(`{"type":"text"}`), changePlanRaw(`{"type":"text"}`), ""}, {"unknown", "field", nil, nil, ""}} {
		if got := ChangePlanExpectedChangeKind(test.operation, test.resourceType, test.before, test.after); got != test.want {
			t.Fatalf("kind %s/%s = %q, want %q", test.operation, test.resourceType, got, test.want)
		}
	}
}

func TestChangePlanSensitiveUpdateAndRiskHelpers(t *testing.T) {
	if ChangePlanSensitiveUpdate("create", "role", nil, nil) {
		t.Fatal("non-update marked sensitive")
	}
	for _, resourceType := range []string{"role", "permission", "data_scope", "field_permission", "menu", "workflow", "identity.role", "identity.role_permission", "identity.role_data_scope", "identity.role_field_permission", "identity.menu"} {
		if !ChangePlanSensitiveUpdate("update", " "+resourceType+" ", nil, nil) {
			t.Fatalf("%s update must be sensitive", resourceType)
		}
	}
	if ChangePlanSensitiveUpdate("update", "object", nil, nil) {
		t.Fatal("ordinary unchanged resource marked sensitive")
	}
	if !ChangePlanSensitiveUpdate("update", "field", changePlanRaw(`{"required":false}`), changePlanRaw(`{"required":true}`)) || !ChangePlanSensitiveUpdate("update", "field", changePlanRaw(`{"options":[1,2]}`), changePlanRaw(`{"options":[1]}`)) {
		t.Fatal("breaking field update not sensitive")
	}

	if changePlanFieldTypeChanged("object", nil, nil) || changePlanFieldTypeChanged("field", changePlanRaw(`{"type":""}`), changePlanRaw(`{"type":"text"}`)) || changePlanFieldTypeChanged("field", changePlanRaw(`{"type":"text"}`), changePlanRaw(`{"type":""}`)) {
		t.Fatal("incomplete field types marked changed")
	}
	if changePlanFieldBecameRequired("object", nil, nil) || changePlanFieldBecameRequired("field", changePlanRaw(`{"required":"false"}`), changePlanRaw(`{"required":true}`)) || changePlanFieldBecameRequired("field", changePlanRaw(`{"required":true}`), changePlanRaw(`{"required":true}`)) {
		t.Fatal("invalid required transition marked breaking")
	}
	if changePlanFieldOptionsShrank("object", nil, nil) || changePlanFieldOptionsShrank("field", changePlanRaw(`{"options":"a"}`), changePlanRaw(`{"options":[]}`)) || changePlanFieldOptionsShrank("field", changePlanRaw(`{"options":[1]}`), changePlanRaw(`{"options":[1,2]}`)) || changePlanFieldOptionsShrank("field", changePlanRaw(`{"options":[1]}`), changePlanRaw(`{"options":[1]}`)) {
		t.Fatal("non-shrinking options marked breaking")
	}
	if got := changePlanJSONMap(changePlanRaw(`invalid`)); len(got) != 0 {
		t.Fatalf("invalid JSON map = %#v", got)
	}
	if changePlanString(" value ") != " value " || changePlanString(1) != "" {
		t.Fatal("change plan string conversion mismatch")
	}
}

func TestChangePlanCanonicalResourceType(t *testing.T) {
	for input, want := range map[string]string{" automation_rule ": "automation", " field ": "field", "": ""} {
		if got := ChangePlanCanonicalResourceType(input); got != want {
			t.Fatalf("canonical %q = %q, want %q", input, got, want)
		}
	}
	if !reflect.DeepEqual(changePlanJSONMap(changePlanRaw(`{"key":"value"}`)), map[string]any{"key": "value"}) {
		t.Fatal("valid JSON map mismatch")
	}
}

func TestChangePlanResourceOwnerNormalizesStorageProvenance(t *testing.T) {
	for sourceKind, want := range map[string]string{
		"generated": "template", "manifest": "template", "builder_v4": "builder", "user": "manual", "runtime": "platform", "plugin": "plugin", "invented": "unknown",
	} {
		if got := ChangePlanResourceOwnerForSourceKind(sourceKind); got != want {
			t.Fatalf("source kind %q owner=%q want=%q", sourceKind, got, want)
		}
	}
}
