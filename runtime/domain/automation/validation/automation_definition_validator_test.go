package validation

import (
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestValidateConditionGroupReturnsFieldPath(t *testing.T) {
	err := AutomationValidateConditionGroup(automationmodel.AutomationConditionGroup{
		Mode: "none",
	}, "conditions", 0)
	assertDefinitionError(t, err, apperror.KindBadRequest, "backend.automation.condition_mode_invalid", "conditions.mode")
}

func TestValidateOperationInputRejectsProtocolTypeMismatch(t *testing.T) {
	err := AutomationValidateOperationInput(connectormodel.ConnectorOperationSchema{
		Key: "charge",
		Input: []definitionmodel.FieldSchema{{
			Key:  "amount",
			Type: "integer",
		}}}, map[string]any{"amount": 42.5}, definitionmodel.ObjectSchema{}, nil)
	assertDefinitionError(t, err, apperror.KindBadRequest, "backend.automation.operation_input_type_mismatch", "amount")
}

func assertDefinitionError(t *testing.T, err error, kind apperror.ErrorKind, code, field string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("unexpected error kind/code: %#v", appErr)
	}
	if field != "" && appErr.Params["field"] != field {
		t.Fatalf("unexpected field params: %#v", appErr.Params)
	}
}
