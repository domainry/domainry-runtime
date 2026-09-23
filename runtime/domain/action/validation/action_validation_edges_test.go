package validation

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionPayloadObjectAndFieldShapeMatrix(t *testing.T) {
	object, hasContract := ActionPayloadObject(definitionmodel.ActionSchema{Key: "order.submit", Label: "Submit"})
	if hasContract || object.Key != "order.submit:payload" || object.Name != "Submit Payload" || len(object.Fields) != 0 {
		t.Fatalf("empty payload object = %#v/%v", object, hasContract)
	}
	typed := definitionmodel.ActionSchema{Key: "typed", PayloadFields: []definitionmodel.ActionPayloadField{{Key: ""}, {Key: " amount ", Name: " Amount ", Type: " number ", Options: []string{"one"}, Required: true, DefaultValue: 1}}}
	object, hasContract = ActionPayloadObject(typed)
	if !hasContract || len(object.Fields) != 1 || object.Fields[0].Key != "amount" || object.Fields[0].Name != "Amount" || object.Fields[0].Type != "number" || !object.Fields[0].Required || object.Fields[0].DefaultValue != 1 || !reflect.DeepEqual(object.Fields[0].Validation.Options, []string{"one"}) {
		t.Fatalf("typed payload object = %#v/%v", object, hasContract)
	}
	typed.PayloadFields[1].Options[0] = "changed"
	if object.Fields[0].Validation.Options[0] != "one" {
		t.Fatal("typed payload options alias source")
	}
	if got := ActionTypedPayloadField(definitionmodel.ActionPayloadField{}); got.Key != "" {
		t.Fatalf("blank typed field = %#v", got)
	}
	if got := ActionTypedPayloadField(definitionmodel.ActionPayloadField{Key: "note"}); got.Name != "note" || got.Type != "text" {
		t.Fatalf("default typed field = %#v", got)
	}
}

func TestActionDefinitionValidationIdentityKindAndIssueEdges(t *testing.T) {
	issues := ActionValidateDefinitionIssues(definitionmodel.ActionSchema{})
	if len(issues) != 3 {
		t.Fatalf("empty action issues = %#v", issues)
	}
	for _, kind := range definitionmodel.ActionKindValues() {
		if !actionSupportedKind(" " + kind + " ") {
			t.Fatalf("kind %q rejected", kind)
		}
	}
	if actionSupportedKind("unknown") {
		t.Fatal("unknown kind accepted")
	}
	issue := actionDefinitionValidationIssue("backend.action.definition_invalid", "", nil)
	if issue.ErrorCode != "backend.action.definition_invalid" || issue.MessageKey != issue.ErrorCode {
		t.Fatalf("fallback issue = %#v", issue)
	}
	if issue := actionDefinitionValidationIssue("backend.action.unknown", "field", nil); issue.ErrorCode != "backend.action.unknown" || issue.FieldPath != "field" {
		t.Fatalf("unknown action issue = %#v", issue)
	}
}

func TestActionPreconditionExpressionCompleteGrammarMatrix(t *testing.T) {
	for _, value := range []string{"current stage is terminal", "status not in closed,lost", "status != open", "status == arbitrary prose", "amount <= 2", "amount < other_field"} {
		if err := ActionValidatePreconditionExpression(value); err != nil {
			t.Fatalf("valid expression %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "Bad Field is present", "Bad Field in one", "status in one,", "Bad Field >= 1", "status == ''", "amount > invalid value", "unsupported prose"} {
		if err := ActionValidatePreconditionExpression(value); err == nil {
			t.Fatalf("invalid expression %q accepted", value)
		}
	}
	if err := actionValidatePreconditionField("valid.field-1"); err != nil {
		t.Fatal(err)
	}
}
