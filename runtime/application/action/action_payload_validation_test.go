package action

import (
	"testing"

	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestTypedPayloadFieldPreservesSelectOptions(t *testing.T) {
	field := actionvalidation.ActionTypedPayloadField(definitionmodel.ActionPayloadField{Key: "method", Type: "select", Required: true, Options: []string{"original_payment", "store_credit"}})
	if len(field.Validation.Options) != 2 || field.Validation.Options[0] != "original_payment" || field.Validation.Options[1] != "store_credit" {
		t.Fatalf("select payload options were not preserved: %#v", field)
	}
}
