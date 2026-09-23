package projectmodel

import (
	"encoding/json"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestCompactFieldSyntax(t *testing.T) {
	checks := []struct {
		declaration string
		typeName    string
		required    bool
		unique      bool
		target      string
		options     int
		defaultJSON string
	}{
		{declaration: "text!;max_length=120", typeName: "text", required: true},
		{declaration: "email!;unique;max_length=254", typeName: "email", required: true, unique: true},
		{declaration: "relation!->customer", typeName: "relation", required: true, target: "customer"},
		{declaration: "select![lead|won]=lead", typeName: "select", required: true, options: 2, defaultJSON: `"lead"`},
		{declaration: "integer!;min=1", typeName: "integer", required: true},
		{declaration: "boolean!=true;sensitive", typeName: "boolean", required: true, defaultJSON: "true"},
	}
	for _, check := range checks {
		t.Run(check.declaration, func(t *testing.T) {
			var field Field
			encoded, _ := json.Marshal(check.declaration)
			if err := json.Unmarshal(encoded, &field); err != nil {
				t.Fatal(err)
			}
			if field.Type != check.typeName || field.Required != check.required || field.Unique != check.unique || len(field.Validation.Options) != check.options || string(field.Default) != check.defaultJSON {
				t.Fatalf("field=%#v", field)
			}
			if check.target != "" && (field.Relation == nil || field.Relation.TargetObjectKey != check.target) {
				t.Fatalf("relation=%#v", field.Relation)
			}
		})
	}
}

func TestCompactFieldRejectsAmbiguousOrVerboseDeclarations(t *testing.T) {
	for _, source := range []string{
		`{"name":"Name","type":"text","required":true}`,
		`"text!;max_length=12;max_length=30"`,
		`"relation!"`,
		`"select![lead|lead]"`,
		`"select![lead|won]=lost"`,
		`"integer!=true"`,
		`"text!;unknown=value"`,
	} {
		var field Field
		if err := json.Unmarshal([]byte(source), &field); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
}

func TestCompactPermissionSyntax(t *testing.T) {
	var simple RolePermission
	if err := json.Unmarshal([]byte(`"customer.read;all"`), &simple); err != nil {
		t.Fatal(err)
	}
	if simple.PermissionKey != "customer.read" || simple.DataScope != identitysdk.DataScopeAll {
		t.Fatalf("permission=%#v", simple)
	}
	var audited RolePermission
	if err := json.Unmarshal([]byte(`{"permission_key":"customer.read","data_scope":"all","audit_denial":true}`), &audited); err != nil {
		t.Fatal(err)
	}
	if !audited.AuditDenial || audited.DataScope != identitysdk.DataScopeAll {
		t.Fatalf("audited permission=%#v", audited)
	}
	for _, source := range []string{
		`{"permission_key":"customer.read","data_scope":"all"}`,
		`"customer.read"`,
		`"customer.read;unknown"`,
		`"customer.read;all;owner"`,
	} {
		var permission RolePermission
		if err := json.Unmarshal([]byte(source), &permission); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
}

func TestDecodeDerivesFieldNameFromCompactMapKey(t *testing.T) {
	model, err := Decode([]byte(validModelJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if field := model.Objects["customer"].Fields["name"]; field.Key != "name" || field.Name != "name" {
		t.Fatalf("field=%#v", field)
	}
	if !strings.Contains(validModelJSON(), `"name":"text!"`) {
		t.Fatal("fixture stopped using compact field syntax")
	}
}
