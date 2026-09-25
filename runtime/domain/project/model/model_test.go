package projectmodel

import (
	"strings"
	"testing"
)

func validModelJSON() string {
	return `{
  "schema_version":"1",
  "project":{"key":"crm","name":"CRM","default_locale":"zh-CN","time_zone":"Asia/Shanghai","initial_workspace_administrator_role":"administrator"},
  "objects":{"customer":{"name":"Customer","description":"","fields":{"name":"text!"}}},
  "roles":{"administrator":{"name":"Administrator","permissions":["customer.read;all"]}},
  "identity_profiles":{}
}`
}

func TestDecodeAcceptsOnlyMinimalClosedModel(t *testing.T) {
	model, err := Decode([]byte(validModelJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if model.Objects["customer"].Key != "customer" || model.Roles["administrator"].Key != "administrator" {
		t.Fatalf("map identities were not materialized: %#v", model)
	}
	objects, err := RuntimeObjects(model)
	if err != nil || len(objects) != 1 || len(objects[0].Fields) != 1 || objects[0].Fields[0].Key != "name" {
		t.Fatalf("runtime objects=%#v err=%v", objects, err)
	}
	first, err := ContentHash(model)
	if err != nil || first == "" {
		t.Fatalf("hash=%q err=%v", first, err)
	}
	second, err := ContentHash(model)
	if err != nil || second != first {
		t.Fatalf("hash changed: %q != %q (%v)", second, first, err)
	}
}

func TestDecodeAcceptsIdentityOnlyModelWithoutBusinessObjects(t *testing.T) {
	model, err := Decode([]byte(`{
  "schema_version":"1",
  "project":{"key":"identity_shell","name":"Identity Shell","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Objects) != 0 || model.Project.InitialWorkspaceAdministratorRole != "administrator" {
		t.Fatalf("model = %#v", model)
	}
}

func TestDecodeRejectsDuplicateUnknownBehaviorAndPresentationFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "duplicate", raw: `{"schema_version":"1","schema_version":"1"}`, want: "project_model.duplicate_key"},
		{name: "unknown behavior", raw: strings.Replace(validModelJSON(), `"identity_profiles":{}`, `"identity_profiles":{},"reports":{}`, 1), want: `unknown field "reports"`},
		{name: "frontend ux", raw: strings.Replace(validModelJSON(), `"description":""`, `"description":"","ux":{"display":{"title_field":"name"}}`, 1), want: `unknown field "ux"`},
		{name: "trailing", raw: validModelJSON() + `{}`, want: "project_model.trailing_value"},
		{name: "wrong version", raw: strings.Replace(validModelJSON(), `"schema_version":"1"`, `"schema_version":"2"`, 1), want: "project_model.schema_version_unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v want=%q", err, test.want)
			}
		})
	}
}

func TestValidateCrossChecksCodePermissionCatalog(t *testing.T) {
	model, err := Decode([]byte(validModelJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(model, map[string]bool{"customer.read": true}); err != nil {
		t.Fatalf("known permission rejected: %v", err)
	}
	if err := Validate(model, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "project_model.permission_unknown") {
		t.Fatalf("unknown permission err=%v", err)
	}
}

func TestValidateRejectsRuntimeOwnedObjectKeys(t *testing.T) {
	_, err := Decode([]byte(strings.Replace(validModelJSON(), `"customer":{`, `"record_timer":{`, 1)))
	if err == nil || !strings.Contains(err.Error(), "project_model.object_key_reserved") {
		t.Fatalf("reserved object err=%v", err)
	}
}
