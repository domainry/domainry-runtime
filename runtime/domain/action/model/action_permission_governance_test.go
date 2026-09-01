package actionmodel

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionPermissionGovernanceProjection(t *testing.T) {
	ordinary := definitionmodel.ActionSchema{Key: "order.view", ObjectKey: "order", Kind: "object_operation"}
	if ActionRiskLevel(ordinary) != "low" || ActionHasHighRiskEffect(ordinary) || ActionHasEnhancedAssurance(ordinary) {
		t.Fatalf("ordinary=%#v", ordinary)
	}
	critical := definitionmodel.ActionSchema{Key: "order.refund", ObjectKey: "order", Kind: "record_operation", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}}}
	if ActionRiskLevel(critical) != "critical" || ActionHasHighRiskEffect(critical) || !ActionHasEnhancedAssurance(critical) || !ActionApprovalRequired(critical) {
		t.Fatalf("critical=%#v", critical)
	}
}

func TestActionPermissionGovernanceCoversAssuranceRiskAndKindMatrix(t *testing.T) {
	if methods := ActionAssuranceMethods(definitionmodel.ActionSchema{}); methods != nil {
		t.Fatalf("nil assurance methods=%v", methods)
	}
	for _, method := range []string{
		definitionmodel.ActionAssuranceMakerChecker,
		definitionmodel.ActionAssuranceWorkflowApproval,
	} {
		action := definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{method}}}
		if !ActionApprovalRequired(action) {
			t.Fatalf("approval method %q not recognized", method)
		}
	}
	plain := definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}}}
	if ActionApprovalRequired(plain) || ActionHasEnhancedAssurance(plain) {
		t.Fatal("normal login was treated as enhanced approval")
	}

	if got := ActionRiskLevel(definitionmodel.ActionSchema{RiskLevel: "high"}); got != "high" {
		t.Fatalf("declared risk=%q", got)
	}
	if got := ActionRiskLevels(); len(got) != 4 {
		t.Fatalf("risk levels=%v", got)
	}
	for index, risk := range []string{"low", "medium", "high", "critical"} {
		if rank := ActionRiskLevelRank(risk); rank != index+1 {
			t.Fatalf("risk=%s rank=%d", risk, rank)
		}
	}
	if ActionRiskLevelRank("unknown") != 0 {
		t.Fatal("unknown risk has rank")
	}

	for _, test := range []struct {
		action definitionmodel.ActionSchema
		want   string
	}{
		{action: definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceWorkflowApproval}}}, want: "critical"},
		{action: definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}}, want: "high"},
		{action: definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}}}, want: "high"},
		{action: plain, want: "low"},
		{action: definitionmodel.ActionSchema{Kind: "record_delete"}, want: "high"},
		{action: definitionmodel.ActionSchema{Kind: "record_update"}, want: "medium"},
		{action: definitionmodel.ActionSchema{Kind: "record_operation"}, want: "medium"},
		{action: definitionmodel.ActionSchema{Kind: "bulk_operation"}, want: "medium"},
		{action: definitionmodel.ActionSchema{Kind: "object_operation"}, want: "low"},
	} {
		if got := ActionRiskLevel(test.action); got != test.want {
			t.Fatalf("action=%#v risk=%q want=%q", test.action, got, test.want)
		}
	}
	if !ActionHasHighRiskEffect(definitionmodel.ActionSchema{Kind: "record_delete"}) {
		t.Fatal("record delete not high risk")
	}
	for _, method := range []string{definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP} {
		action := definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{method}}}
		if !ActionHasEnhancedAssurance(action) {
			t.Fatalf("enhanced method %q not recognized", method)
		}
	}
}
