package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
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

func TestActionDefinitionValidationAcceptsSourceOwnedActionMetadata(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.complete", ObjectKey: "order", Label: "Complete", Kind: "record_update",
		RequiresPermission: "order.complete", AuditEvent: "order.completed",
		PayloadFields:   []definitionmodel.ActionPayloadField{{Key: "request_id", Type: "text", Required: true}},
		IdempotencyKeys: []string{"request_id"},
	}
	if issues := ActionValidateDefinitionIssues(action); len(issues) != 0 {
		t.Fatalf("source-owned Action metadata rejected: %#v", issues)
	}
}

func TestActionPermissionPolicyValidatesHighRiskGovernance(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "order.delete", ObjectKey: "order", Kind: "record_delete", RequiresPermission: "order.update", AuditEvent: "order.deleted"}
	issues := ActionValidateDefinitionIssues(action)
	for _, code := range []string{"backend.action.high_risk_permission_must_be_dedicated", "backend.action.high_risk_assurance_required"} {
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
		{name: "invalid permission format", action: definitionmodel.ActionSchema{RequiresPermission: "Order Read"}, code: "backend.action.permission_format_invalid"},
		{name: "object mismatch", action: definitionmodel.ActionSchema{ObjectKey: "order", RequiresPermission: "invoice.read"}, code: "backend.action.permission_object_mismatch"},
		{name: "blank object", action: definitionmodel.ActionSchema{RequiresPermission: "order.read"}},
		{name: "matching object", action: definitionmodel.ActionSchema{ObjectKey: "order", RequiresPermission: "order.read"}},
		{name: "invalid declared risk", action: definitionmodel.ActionSchema{ObjectKey: "order", RequiresPermission: "order.read", RiskLevel: "extreme"}, code: "backend.action.risk_level_invalid"},
		{name: "understated risk", action: definitionmodel.ActionSchema{Key: "order.delete", ObjectKey: "order", Kind: "record_delete", RequiresPermission: "order.delete", RiskLevel: "low"}, code: "backend.action.risk_level_understated"},
		{name: "sufficient declared risk", action: definitionmodel.ActionSchema{Key: "order.update", ObjectKey: "order", Kind: "record_update", RequiresPermission: "order.update", RiskLevel: "high", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}}}},
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

func actionIssuesContainCode(issues []metadatamodel.MetadataDefinitionValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.ErrorCode == code {
			return true
		}
	}
	return false
}
