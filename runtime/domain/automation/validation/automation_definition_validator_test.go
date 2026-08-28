package validation

import (
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestValidateConditionGroupReturnsFieldPath(t *testing.T) {
	err := AutomationValidateConditionGroup(automationmodel.AutomationConditionGroup{
		Mode: "none",
	}, "conditions", 0)
	assertDefinitionError(t, err, apperror.KindBadRequest, "backend.automation.condition_mode_invalid", "conditions.mode")
}

func TestValidateOperationInputRejectsProtocolTypeMismatch(t *testing.T) {
	err := AutomationValidateOperationInput(integrationmodel.ConnectorOperationSchema{
		Key: "charge",
		Input: []definitionmodel.FieldSchema{{
			Key:  "amount",
			Type: "integer",
		}}}, map[string]any{"amount": 42.5}, definitionmodel.ObjectSchema{}, nil)
	assertDefinitionError(t, err, apperror.KindBadRequest, "backend.automation.operation_input_type_mismatch", "amount")
}

func TestValidateActiveInstructionIssuesLocatesInstructionType(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Instructions: []automationmodel.AutomationInstructionSchema{{Key: "send", Type: "integration_call"}}}
	issues := AutomationValidateActiveInstructionIssues(rule)
	if len(issues) != 1 {
		t.Fatalf("expected one issue, got %#v", issues)
	}
	issue := issues[0]
	if issue.ErrorCode != "backend.automation.instruction_type_invalid" || issue.FieldPath != "instructions[0].type" || issue.InstructionKey != "send" {
		t.Fatalf("unexpected issue: %#v", issue)
	}
	if issue.ContractVersion != AutomationRuntimeAuthoringContractVersion {
		t.Fatalf("unexpected contract version: %q", issue.ContractVersion)
	}
}

func TestHasPermissionSupportsDedicatedAndLegacyPermissions(t *testing.T) {
	for _, permission := range []string{"automation.rule.write", "automation.*", "automation.manage"} {
		principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{permission}})
		if !AutomationHasPermission(principal, "manage") {
			t.Fatalf("expected %q to grant manage", permission)
		}
	}
	if AutomationHasPermission(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}}), "manage") {
		t.Fatal("unknown principal must not receive automation permission")
	}
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
