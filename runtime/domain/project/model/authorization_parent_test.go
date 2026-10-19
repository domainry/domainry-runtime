package projectmodel

import (
	"strings"
	"testing"
)

func TestClosedModelAuthorizationParentContract(t *testing.T) {
	objects := `"objects":{"customer":{"name":"Customer","fields":{"name":"text!"}},"event":{"name":"Event","capabilities":{"read":true},"authorization_parent":{"object_key":"customer","relation_field":"customer"},"fields":{"customer":"relation!->customer"}}}`
	raw := strings.Replace(validModelJSON(), `"objects":{"customer":{"name":"Customer","description":"","fields":{"name":"text!"}}}`, objects, 1)
	model, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := RuntimeObjects(model)
	if err != nil || projected[1].AuthorizationParent == nil || projected[1].AuthorizationParent.ObjectKey != "customer" {
		t.Fatalf("ownership not projected: %+v %v", projected, err)
	}
	for _, invalid := range []string{
		strings.Replace(raw, `"relation!->customer"`, `"relation->customer"`, 1),
		strings.Replace(raw, `"object_key":"customer"`, `"object_key":"event"`, 1),
		strings.Replace(raw, `"object_key":"customer"`, `"object_key":"missing"`, 1),
		strings.Replace(raw, `"read":true`, `"read":true,"create":true`, 1),
		strings.Replace(raw, `"relation_field":"customer"`, `"relation_field":"missing"`, 1),
	} {
		if _, err := Decode([]byte(invalid)); err == nil || !strings.Contains(err.Error(), "project_model.authorization_parent_invalid") {
			t.Fatalf("invalid ownership accepted: %v", err)
		}
	}
}
