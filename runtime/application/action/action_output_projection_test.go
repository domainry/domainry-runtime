package action

import (
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestProjectBusinessHandlerOutputReappliesFieldSecurity(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "email", Type: "email", Config: map[string]any{"sensitive": true}},
		{Key: "token", Type: "text", Config: map[string]any{"sensitive": true}},
	}}
	action := definitionmodel.ActionSchema{Key: "customer.inspect", OutputFields: []definitionmodel.ActionOutputField{
		{Key: "email", SourceObjectKey: "customer", SourceFieldKey: "email"},
		{Key: "tokens", SourceObjectKey: "customer", SourceFieldKey: "token", Repeated: true},
		{Key: "summary", Type: "text"},
	}}
	objects := func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key }
	input := map[string]any{"email": "person@example.test", "tokens": []any{"secret-one", "secret-two"}, "summary": "safe"}

	denied, err := ProjectBusinessHandlerOutput(t.Context(), accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{}), action, input, objects)
	if err != nil || !reflect.DeepEqual(denied, map[string]any{"summary": "safe"}) {
		t.Fatalf("denied projection=%#v err=%v", denied, err)
	}

	maskedRole := accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "customer", FieldKey: "email", Read: true, Masked: true},
		{ObjectKey: "customer", FieldKey: "token", Read: true, Masked: true},
	}}
	masked, err := ProjectBusinessHandlerOutput(t.Context(), accessfixture.Attach(principalmodel.Principal{}, maskedRole), action, input, objects)
	if err != nil || masked["email"] != "****@example.test" || !reflect.DeepEqual(masked["tokens"], []any{"****-one", "****-two"}) || masked["summary"] != "safe" {
		t.Fatalf("masked projection=%#v err=%v", masked, err)
	}

	contextualRole := maskedRole
	contextualRole.FieldPolicies[0].Policies = []accessfixture.FieldRuleFixture{{
		Actions: []string{"read"}, Effect: "allow",
		Predicate: &accessfixture.PredicateFixture{Operator: "equal", FieldKey: "owner_id", Values: []string{"user-a"}},
	}}
	contextual, err := ProjectBusinessHandlerOutput(t.Context(), accessfixture.Attach(principalmodel.Principal{}, contextualRole), action, input, objects)
	if err != nil || contextual["email"] != nil {
		t.Fatalf("contextual value must fail closed without record context: %#v err=%v", contextual, err)
	}
}

func TestProjectBusinessHandlerOutputRejectsBrokenLineage(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "customer.inspect", OutputFields: []definitionmodel.ActionOutputField{{Key: "email", SourceObjectKey: "customer"}}}
	if _, err := ProjectBusinessHandlerOutput(t.Context(), principalmodel.Principal{}, action, map[string]any{"email": "secret"}, nil); err == nil {
		t.Fatal("broken output lineage was accepted")
	}
}
