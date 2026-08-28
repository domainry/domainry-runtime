package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordSubjectLifecycleRequiresExplicitNonUserIdentityAndErasePolicy(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "profile", Fields: []definitionmodel.FieldSchema{
		{Key: "user", Type: "user"},
		{Key: "customer_ref", Type: "text", Config: map[string]any{"lifecycle_subject_identity": true}},
		{Key: "email", Type: "email", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "attachment", Type: "text", Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "delete"}},
	}}
	fields := RecordSubjectIdentityFields(object)
	if len(fields) != 2 || fields[0].Key != "user" || fields[1].Key != "customer_ref" {
		t.Fatalf("identity fields=%#v", fields)
	}
	if !RecordSubjectFileField(object.Fields[3]) || RecordSubjectEraseMode(object.Fields[2]) != RecordLifecycleEraseAnonymize {
		t.Fatal("lifecycle field policy not projected")
	}
	if err := RecordValidateSubjectLifecycle(object); err != nil {
		t.Fatal(err)
	}
}

func TestRecordSubjectLifecycleRejectsAmbiguousConfiguration(t *testing.T) {
	tests := []definitionmodel.FieldSchema{
		{Key: "identity", Config: map[string]any{"lifecycle_subject_identity": "yes"}},
		{Key: "file", Config: map[string]any{"lifecycle_subject_file": "yes"}},
		{Key: "secret", Config: map[string]any{"lifecycle_erase": "scrub"}},
		{Key: "file", Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "anonymize"}},
	}
	for _, field := range tests {
		if err := RecordValidateSubjectLifecycle(definitionmodel.ObjectSchema{Key: "profile", Fields: []definitionmodel.FieldSchema{field}}); err == nil {
			t.Fatalf("invalid lifecycle config accepted: %#v", field.Config)
		}
	}
}

func TestRecordSubjectIdentityFieldsConfiguredFalseAndPlainField(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "disabled_user", Type: "user", Config: map[string]any{"lifecycle_subject_identity": false}},
		{Key: "plain", Type: "text"},
	}}
	if fields := RecordSubjectIdentityFields(object); len(fields) != 0 {
		t.Fatalf("identity fields = %#v", fields)
	}
}
