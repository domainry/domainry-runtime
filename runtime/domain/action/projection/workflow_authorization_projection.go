package projection

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

// AuthorizationActionForWorkflow projects one executable Workflow into the
// same source-owned Action/Permission model used by authored business Actions.
// The generic HTTP {workflowKey} route is only a binding resolver and never a
// permission factory.
func AuthorizationActionForWorkflow(workflow definitionmodel.WorkflowSchema, owner string) (actioncontract.ActionDefinition, error) {
	workflowKey := strings.TrimSpace(workflow.Key)
	actionKey := workflowcontract.RunActionKey(workflowKey)
	if actionKey == "" {
		return actioncontract.ActionDefinition{}, fmt.Errorf("Workflow key is required for authorization Action projection")
	}
	label := strings.TrimSpace(workflow.Name)
	if label == "" {
		label = workflowKey
	}
	definition := actioncontract.ActionDefinition{
		Key: actionKey, Owner: strings.TrimSpace(owner), SourceKind: "metadata_workflow_action",
		CapabilityKey: "workflow." + workflowKey, CapabilityLabel: label,
		OperationKey: "run", OperationLabel: "Run", Label: "Run " + label,
		Exposures:     []actioncontract.Exposure{actioncontract.ExposurePublic},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		NonHTTP: []actioncontract.NonHTTPBinding{{
			Kind: workflowcontract.RunActionBindingKind, InvocationKey: workflowKey,
		}},
		Permission: &actioncontract.PermissionDefinition{
			Key: actionKey, Owner: strings.TrimSpace(owner), ResourceKey: "workflow." + workflowKey, OperationKey: "run",
			Label: "Run " + label, Description: "Invoke the published Workflow " + label,
			Category: "Business Workflows", LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectWrite, RiskLevel: actioncontract.RiskMedium,
		IdempotencyDecision: "caller_key_required", AuditClass: "workflow_execution",
		LifecycleStatus: actioncontract.LifecycleActive,
	}
	normalized, err := actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return actioncontract.ActionDefinition{}, fmt.Errorf("project Workflow %q authorization Action: %w", workflowKey, err)
	}
	return normalized, nil
}
