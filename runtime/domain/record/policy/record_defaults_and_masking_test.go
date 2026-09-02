package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordOwnerDefaultsAreRuntimeMetadata(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "status", Default: "new"},
	}}
	record := recordmodel.Record{Data: map[string]any{}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", OrgID: "org-1"}}
	RecordApplyOwnerDefault(&record, principal)
	RecordApplyFieldDefaults(object, record.Data)
	if record.OwnerUserID != "user-1" || record.OwnerOrgID != "org-1" || record.Data["status"] != "new" || len(record.Data) != 1 {
		t.Fatalf("record=%#v", record)
	}
	preserved := recordmodel.Record{OwnerUserID: "other", OwnerOrgID: "org-2", Data: map[string]any{"status": "active"}}
	RecordApplyOwnerDefault(&preserved, principal)
	RecordApplyFieldDefaults(object, preserved.Data)
	if preserved.OwnerUserID != "other" || preserved.OwnerOrgID != "org-2" || preserved.Data["status"] != "active" {
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
