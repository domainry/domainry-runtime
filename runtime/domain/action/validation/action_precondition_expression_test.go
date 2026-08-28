package validation

import (
	"strings"
	"testing"
)

func TestActionValidatePreconditionExpressionRejectsIgnoredProse(t *testing.T) {
	for _, valid := range []string{"status == open", "status in draft,open", "amount > 0", "available >= requested", "due_at >= today", "owner is present"} {
		if err := ActionValidatePreconditionExpression(valid); err != nil {
			t.Fatalf("valid precondition %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"available balance covers requested days", "response deadline has not passed", "buy route requires vendor"} {
		if err := ActionValidatePreconditionExpression(invalid); err == nil {
			t.Fatalf("unsupported prose %q was accepted", invalid)
		}
	}
}

func TestActionParsePreconditionExpressionExposesValidatedGrammar(t *testing.T) {
	tests := []struct {
		input, field, operator string
		operands               []string
	}{
		{" Status == READY ", "status", "==", []string{"ready"}},
		{"status in READY, draft", "status", "in", []string{"ready", "draft"}},
		{"amount > other_amount", "amount", ">", []string{"other_amount"}},
		{"customer is present", "customer", "is present", nil},
		{"current stage is terminal", "", "constant", []string{"current stage is terminal"}},
	}
	for _, test := range tests {
		expression, err := ActionParsePreconditionExpression(test.input)
		if err != nil {
			t.Fatalf("parse %q: %v", test.input, err)
		}
		if expression.Field != test.field || expression.Operator != test.operator || strings.Join(expression.Operands, ",") != strings.Join(test.operands, ",") {
			t.Fatalf("parse %q = %+v", test.input, expression)
		}
		if err := ActionValidatePreconditionExpression(test.input); err != nil {
			t.Fatalf("validator diverged for %q: %v", test.input, err)
		}
	}
}
