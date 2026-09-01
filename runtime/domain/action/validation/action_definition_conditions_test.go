package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionAssuranceDefinitionValidatesKnownObjectFields(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.approve", ObjectKey: "order", Kind: "record_operation", AuditEvent: "order.approved",
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceWorkflowApproval}, ApprovalVersionField: "missing", ApprovalHashField: "approval_hash"},
	}
	issues := ActionValidateDefinitionIssuesWithObjects(action, []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "approval_hash"}}}})
	if len(issues) == 0 {
		t.Fatal("unknown assurance field was accepted")
	}
}

func TestActionAssurancePolicyCoversMethodAndFieldMatrix(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{
		{Key: "approval_version"}, {Key: "approval_hash"}, {Key: "maker_id"},
	}}
	action := definitionmodel.ActionSchema{ObjectKey: "order"}
	cases := []struct {
		name      string
		policy    definitionmodel.ActionAssurancePolicy
		objects   []definitionmodel.ObjectSchema
		minIssues int
	}{
		{name: "empty methods", policy: definitionmodel.ActionAssurancePolicy{}, minIssues: 1},
		{name: "unknown and duplicate methods", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"unknown", "unknown"}}, minIssues: 3},
		{name: "recent reauth too small", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}}, minIssues: 1},
		{name: "recent reauth too large", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}, RecentReauthMaxAgeSeconds: 86401}, minIssues: 1},
		{name: "recent reauth valid", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}, RecentReauthMaxAgeSeconds: 60}},
		{name: "age without method", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, RecentReauthMaxAgeSeconds: 60}, minIssues: 1},
		{name: "approval blank fields", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceWorkflowApproval}}, objects: []definitionmodel.ObjectSchema{object}, minIssues: 2},
		{name: "approval unknown fields", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceWorkflowApproval}, ApprovalVersionField: "missing_version", ApprovalHashField: "missing_hash"}, objects: []definitionmodel.ObjectSchema{object}, minIssues: 2},
		{name: "approval fields without known object", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceWorkflowApproval}, ApprovalVersionField: "anything", ApprovalHashField: "anything"}},
		{name: "version field without approval", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalVersionField: "approval_version"}, minIssues: 1},
		{name: "hash field without approval", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalHashField: "approval_hash"}, minIssues: 1},
		{name: "maker blank field", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}}, objects: []definitionmodel.ObjectSchema{object}, minIssues: 1},
		{name: "maker unknown field", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}, MakerField: "missing"}, objects: []definitionmodel.ObjectSchema{object}, minIssues: 1},
		{name: "maker valid field", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}, MakerField: "maker_id"}, objects: []definitionmodel.ObjectSchema{object}},
		{name: "maker field without method", policy: definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, MakerField: "maker_id"}, minIssues: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action.AssurancePolicy = &tc.policy
			issues := actionValidateAssurancePolicy(action, tc.objects)
			if len(issues) < tc.minIssues {
				t.Fatalf("issues=%#v want at least %d", issues, tc.minIssues)
			}
		})
	}
}

func TestActionDefinitionValueTypeHelpersCoverEverySwitchArm(t *testing.T) {
	for _, value := range []string{"boolean", "date", "datetime", "decimal", "duration", "integer", "text"} {
		if !actionRuleSetValueType(" " + value + " ") {
			t.Fatalf("rule set type %q rejected", value)
		}
	}
	if actionRuleSetValueType("json") {
		t.Fatal("unsupported rule set type accepted")
	}
	for _, value := range []string{"boolean", "date", "datetime", "decimal", "integer", "json", "number", "text"} {
		if !actionPreferenceValueType(" " + value + " ") {
			t.Fatalf("preference type %q rejected", value)
		}
	}
	if actionPreferenceValueType("duration") {
		t.Fatal("unsupported preference type accepted")
	}
}
