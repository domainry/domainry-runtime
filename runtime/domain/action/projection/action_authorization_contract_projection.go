package projection

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// AuthorizationContractContext supplies assembly facts that are deliberately
// absent from project-authored ActionSchema. It contains no handler or runtime
// principal and is not persisted back into Metadata.
type AuthorizationContractContext struct {
	Owner              string
	CapabilityKey      string
	CapabilityLabel    string
	PermissionCategory string
	Exposures          []actioncontract.Exposure
	LifecycleStatus    actioncontract.LifecycleStatus
}

// AuthorizationActionDefinition projects the authorization/governance facts
// of one Runtime metadata Action into the shared Action registry contract.
// Execution schemas, preconditions and handler bindings remain source-owned by
// Runtime and are not duplicated in the authorization manifest.
func AuthorizationActionDefinition(schema definitionmodel.ActionSchema, context AuthorizationContractContext) (actioncontract.ActionDefinition, error) {
	owner := strings.TrimSpace(context.Owner)
	lifecycle := context.LifecycleStatus
	if lifecycle == "" {
		lifecycle = actioncontract.LifecycleActive
	}
	permissionKey := strings.TrimSpace(schema.Key)
	resourceKey, operationKey := definitionmodel.ActionPermissionSubject(schema)
	label := strings.TrimSpace(schema.Label)
	if label == "" {
		label = strings.TrimSpace(schema.Key)
	}
	permissionLifecycle := actioncontract.LifecycleActive
	if lifecycle == actioncontract.LifecycleRetired {
		permissionLifecycle = actioncontract.LifecycleRetired
	}
	definition := actioncontract.ActionDefinition{
		Key: strings.TrimSpace(schema.Key), Owner: owner, SourceKind: "metadata_action",
		CapabilityKey: strings.TrimSpace(context.CapabilityKey), CapabilityLabel: strings.TrimSpace(context.CapabilityLabel),
		OperationKey: operationKey, OperationLabel: label, Label: label,
		Exposures:     append([]actioncontract.Exposure(nil), context.Exposures...),
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission},
		NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: "runtime_action", InvocationKey: strings.TrimSpace(schema.Key)}},
		Permission: &actioncontract.PermissionDefinition{
			Key: permissionKey, Owner: owner, ResourceKey: resourceKey, OperationKey: operationKey,
			Label: label, Category: strings.TrimSpace(context.PermissionCategory), LifecycleStatus: permissionLifecycle,
		},
		EffectClass: actioncontract.EffectWrite, RiskLevel: actioncontract.RiskLevel(actionmodel.ActionRiskLevel(schema)),
		AssuranceRequired: actionmodel.ActionAssuranceMethods(schema), ApprovalPolicies: actionApprovalPolicies(schema),
		IdempotencyDecision: "caller_key_required", AuditClass: "business_action", AuditEvent: strings.TrimSpace(schema.AuditEvent), LifecycleStatus: lifecycle,
	}
	normalized, err := actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return actioncontract.ActionDefinition{}, fmt.Errorf("Runtime metadata action %q: %w", schema.Key, err)
	}
	return normalized, nil
}

func actionApprovalPolicies(schema definitionmodel.ActionSchema) []actioncontract.ApprovalPolicy {
	result := []actioncontract.ApprovalPolicy{}
	for _, method := range actionmodel.ActionAssuranceMethods(schema) {
		switch strings.TrimSpace(method) {
		case definitionmodel.ActionAssuranceMakerChecker:
			result = append(result, actioncontract.ApprovalMakerChecker)
		case definitionmodel.ActionAssuranceWorkflowApproval:
			result = append(result, actioncontract.ApprovalWorkflow)
		}
	}
	return result
}
