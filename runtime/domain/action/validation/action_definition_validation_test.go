package validation

import (
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionDefinitionValidationOwnsMetadataContract(t *testing.T) {
	action := definitionmodel.ActionSchema{Kind: "script"}
	issues := ActionValidateDefinitionIssues(action)
	for _, code := range []string{"backend.action.definition_invalid", "backend.action.kind_invalid"} {
		if !actionIssuesContainCode(issues, code) {
			t.Fatalf("issues=%#v missing=%s", issues, code)
		}
	}
	for _, issue := range issues {
		if issue.MessageKey == "" || issue.CapabilityKey != "action.definition" || issue.ContractVersion == "" {
			t.Fatalf("issue=%#v", issue)
		}
	}
}

func TestActionOrganizationUnitDeliveryValidationClosesOperationTypeAndParent(t *testing.T) {
	valid := definitionmodel.ActionSchema{
		Key: "department_profile.provision", ObjectKey: "department_profile", Kind: definitionmodel.ActionKindObjectOperation,
		TargetOrganization: &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceDeliveredOrganizationUnit},
		OrganizationUnitDelivery: &definitionmodel.ActionOrganizationUnitDeliveryPolicy{
			Operations: []string{"create"}, NodeTypes: []string{"department"}, ParentSource: "workspace_company",
		},
	}
	if issues := actionValidateOrganizationUnitDeliveryPolicy(valid); len(issues) != 0 {
		t.Fatalf("valid policy issues=%#v", issues)
	}
	for _, test := range []struct {
		name   string
		mutate func(*definitionmodel.ActionSchema)
		path   string
	}{
		{name: "store bypass", mutate: func(action *definitionmodel.ActionSchema) {
			action.OrganizationUnitDelivery.NodeTypes = []string{"store"}
		}, path: "node_types[0]"},
		{name: "company root", mutate: func(action *definitionmodel.ActionSchema) {
			action.OrganizationUnitDelivery.NodeTypes = []string{"company"}
		}, path: "node_types[0]"},
		{name: "multiple operations", mutate: func(action *definitionmodel.ActionSchema) {
			action.OrganizationUnitDelivery.Operations = []string{"create", "resolve"}
		}, path: "operations"},
		{name: "caller parent", mutate: func(action *definitionmodel.ActionSchema) { action.OrganizationUnitDelivery.ParentSource = "input" }, path: "parent_source"},
		{name: "wrong delivered target", mutate: func(action *definitionmodel.ActionSchema) {
			action.TargetOrganization.Source = definitionmodel.ActionTargetOrganizationSourceProvisionedStore
		}, path: "parent_source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			target := *valid.TargetOrganization
			policy := *valid.OrganizationUnitDelivery
			policy.Operations = append([]string(nil), valid.OrganizationUnitDelivery.Operations...)
			policy.NodeTypes = append([]string(nil), valid.OrganizationUnitDelivery.NodeTypes...)
			candidate.TargetOrganization, candidate.OrganizationUnitDelivery = &target, &policy
			test.mutate(&candidate)
			issues := actionValidateOrganizationUnitDeliveryPolicy(candidate)
			found := false
			for _, issue := range issues {
				found = found || strings.Contains(issue.FieldPath, test.path)
			}
			if !found {
				t.Fatalf("issues=%#v missing path=%s", issues, test.path)
			}
		})
	}
	resolve := valid
	resolve.TargetOrganization = &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceExplicit, Input: definitionmodel.ActionTargetOrganizationInputInvocation}
	resolve.OrganizationUnitDelivery = &definitionmodel.ActionOrganizationUnitDeliveryPolicy{Operations: []string{"resolve"}, NodeTypes: []string{"department"}}
	if issues := actionValidateOrganizationUnitDeliveryPolicy(resolve); len(issues) != 0 {
		t.Fatalf("valid resolve issues=%#v", issues)
	}
}

func TestActionDefinitionValidationAcceptsSourceOwnedActionMetadata(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.complete", ObjectKey: "order", Label: "Complete", Kind: "record_update",
		AuditEvent:    "order.completed",
		PayloadFields: []definitionmodel.ActionPayloadField{{Key: "reason", Type: "text", Required: true}},
	}
	if issues := ActionValidateDefinitionIssues(action); len(issues) != 0 {
		t.Fatalf("source-owned Action metadata rejected: %#v", issues)
	}
}

func TestActionDefinitionValidationAllowsCompilerDefaultAuditEvent(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.submit", ObjectKey: "order", Label: "Submit", Kind: definitionmodel.ActionKindRecordOperation,
	}
	for _, issue := range ActionValidateDefinitionIssues(action) {
		if issue.FieldPath == "audit_event" {
			t.Fatalf("optional audit_event was rejected: %#v", issue)
		}
	}
}

func TestActionDefinitionValidationRejectsRuntimeIdempotencyPayloadField(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.complete", ObjectKey: "order", Label: "Complete", Kind: "record_update", AuditEvent: "order.completed",
		PayloadFields: []definitionmodel.ActionPayloadField{{Key: "idempotency_key", Type: "text"}},
	}
	issues := ActionValidateDefinitionIssues(action)
	if !actionIssuesContainCode(issues, "backend.action.definition_invalid") {
		t.Fatalf("reserved Runtime payload field accepted: %#v", issues)
	}
}

func TestActionPermissionPolicyValidatesHighRiskGovernance(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "order.delete", ObjectKey: "order", Kind: "record_delete", AuditEvent: "order.deleted"}
	issues := ActionValidateDefinitionIssues(action)
	for _, code := range []string{"backend.action.high_risk_assurance_required"} {
		if !actionIssuesContainCode(issues, code) {
			t.Fatalf("issues=%#v missing=%s", issues, code)
		}
	}
}

func TestActionPermissionPolicyCoversFormatObjectAndDeclaredRiskMatrix(t *testing.T) {
	tests := []struct {
		name   string
		action definitionmodel.ActionSchema
		code   string
	}{
		{name: "invalid permission format", action: definitionmodel.ActionSchema{Key: "Order Read"}, code: "backend.action.permission_format_invalid"},
		{name: "object mismatch", action: definitionmodel.ActionSchema{Key: "invoice.read", ObjectKey: "order"}, code: "backend.action.permission_object_mismatch"},
		{name: "blank object", action: definitionmodel.ActionSchema{Key: "order.read"}},
		{name: "matching object", action: definitionmodel.ActionSchema{Key: "order.read", ObjectKey: "order"}},
		{name: "invalid declared risk", action: definitionmodel.ActionSchema{Key: "order.read", ObjectKey: "order", RiskLevel: "extreme"}, code: "backend.action.risk_level_invalid"},
		{name: "understated risk", action: definitionmodel.ActionSchema{Key: "order.delete", ObjectKey: "order", Kind: "record_delete", RiskLevel: "low"}, code: "backend.action.risk_level_understated"},
		{name: "sufficient declared risk", action: definitionmodel.ActionSchema{Key: "order.update", ObjectKey: "order", Kind: "record_update", RiskLevel: "high", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := actionValidatePermissionPolicy(test.action)
			if test.code != "" && !actionIssuesContainCode(issues, test.code) {
				t.Fatalf("issues=%#v missing=%s", issues, test.code)
			}
		})
	}
}

func actionIssuesContainCode(issues []appschemamodel.ApplicationDefinitionValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.ErrorCode == code {
			return true
		}
	}
	return false
}
