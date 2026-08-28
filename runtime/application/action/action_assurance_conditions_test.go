package action

import (
	"context"
	"errors"
	"testing"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

func TestActionInvocationAssuranceValidationConditions(t *testing.T) {
	validated := actionmodel.ActionInvocation{AssuranceValidated: true}
	called := false
	result, err := actionValidateInvocationAssurance(t.Context(), func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
		called = true
		return nil, nil
	}, validated)
	if err != nil || called || !result.AssuranceValidated {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}

	want := errors.New("assurance provider unavailable")
	input := actionmodel.ActionInvocation{}
	result, err = actionValidateInvocationAssurance(t.Context(), func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
		return nil, want
	}, input)
	if !errors.Is(err, want) || result.AssuranceValidated {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
