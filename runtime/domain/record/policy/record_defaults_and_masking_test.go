package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordOwnerAndFieldDefaultsRemainBusinessSchemaBehavior(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "user", Required: true},
		{Key: "owner_department_id", Type: "text"},
		{Key: "owner_department_path", Type: "text"},
		{Key: "status", Default: "new"},
	}}
	data := map[string]any{}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", DepartmentID: "department-1", DepartmentPath: "/company/department-1"}}
	RecordApplyOwnerDefault(object, data, principal)
	RecordApplyFieldDefaults(object, data)
	if data["owner"] != "user-1" || data["owner_department_id"] != "department-1" || data["owner_department_path"] != "/company/department-1" || data["status"] != "new" {
		t.Fatalf("defaults=%#v", data)
	}
	preserved := map[string]any{"owner": "other", "status": "active"}
	RecordApplyOwnerDefault(object, preserved, principal)
	RecordApplyFieldDefaults(object, preserved)
	if preserved["owner"] != "other" || preserved["status"] != "active" {
		t.Fatalf("explicit values changed: %#v", preserved)
	}
}

func TestRecordMaskingAndBooleanHelpers(t *testing.T) {
	if got := RecordMaskFieldValue(definitionmodel.FieldSchema{Type: "email"}, "person@example.test"); got != "****@example.test" {
		t.Fatalf("email mask=%q", got)
	}
	if got := RecordMaskFieldValue(definitionmodel.FieldSchema{Type: "text"}, "12345678"); got != "****5678" {
		t.Fatalf("text mask=%q", got)
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {"yes", true, true}, {"0", false, true}, {"invalid", false, false}, {1, false, false}} {
		got, ok := boolAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("boolAny(%#v)=%v/%v", test.value, got, ok)
		}
	}
}
