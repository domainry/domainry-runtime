package projection

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
)

const operationsActionBindingKind = "runtime_operation"

var operationsOwnedNonHTTPActionKeys = map[string]bool{
	operationscontract.ActionEnableAutomationRule:           true,
	operationscontract.ActionDisableAutomationRule:          true,
	operationscontract.ActionRetryIntegrationEvent:          true,
	operationscontract.ActionReplayIntegrationEvent:         true,
	operationscontract.ActionRetryRuntimePublication:        true,
	operationscontract.ActionReconcileIntegrationInvocation: true,
	operationscontract.ActionCreateBackup:                   true,
	operationscontract.ActionRestoreBackup:                  true,
	operationscontract.ActionDeleteWorkspace:                true,
	operationscontract.ActionEnableMaintenance:              true,
	operationscontract.ActionDisableMaintenance:             true,
	operationscontract.ActionPauseWorkerOwner:               true,
	operationscontract.ActionResumeWorkerOwner:              true,
	operationscontract.ActionDrainRuntimeInstance:           true,
	operationscontract.ActionUndrainRuntimeInstance:         true,
	operationscontract.ActionResolveDeadLetter:              true,
	operationscontract.ActionRetryDeadLetter:                true,
	operationscontract.ActionAcknowledgeDeadLetter:          true,
}

// OperationsAuthorizationActions returns only the non-HTTP Actions owned by
// Runtime Operations. Module and HTTP Actions are not redefined here; their
// operation bindings are attached by OperationsAuthorizationBindings.
func OperationsAuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions := make([]actioncontract.ActionDefinition, 0, len(operationsOwnedNonHTTPActionKeys))
	for _, operation := range operationsDefinitionCatalog {
		if !operationsOwnedNonHTTPActionKeys[operation.ActionKey] {
			continue
		}
		separator := strings.LastIndex(operation.ActionKey, ".")
		if separator <= 0 || separator == len(operation.ActionKey)-1 {
			return nil, fmt.Errorf("operation %q has invalid Action key %q", operation.Kind, operation.ActionKey)
		}
		label := strings.ReplaceAll(operation.Kind, ".", " ")
		definition, err := actioncontract.NormalizeDefinition(actioncontract.ActionDefinition{
			Key: operation.ActionKey, Owner: "runtime:operations", SourceKind: "runtime_operations",
			CapabilityKey: "runtime.operations", CapabilityLabel: "Runtime operations",
			OperationKey: operation.ActionKey[separator+1:], OperationLabel: label, Label: label,
			Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
			Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
			NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: operationsActionBindingKind, InvocationKey: operation.Kind}},
			Permission: &actioncontract.PermissionDefinition{
				Key: operation.ActionKey, Owner: "runtime:operations", ResourceKey: operation.ActionKey[:separator],
				OperationKey: operation.ActionKey[separator+1:], Label: label, Category: "Runtime Operations", LifecycleStatus: actioncontract.LifecycleActive,
			},
			EffectClass: actioncontract.EffectWrite, RiskLevel: actioncontract.RiskHigh,
			ApprovalPolicies:    []actioncontract.ApprovalPolicy{actioncontract.ApprovalReason},
			IdempotencyDecision: "caller_key_required", AuditClass: "mutation_audit_required", AuditEvent: operation.AuditEvent,
			LifecycleStatus: actioncontract.LifecycleActive,
		})
		if err != nil {
			return nil, fmt.Errorf("normalize operation Action %q: %w", operation.ActionKey, err)
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// OperationsAuthorizationBindings projects every operation kind onto exactly
// one already-declared Action. It is the bridge used by the shared registry for
// module Actions, Runtime HTTP Actions, and Operations-owned non-HTTP Actions.
func OperationsAuthorizationBindings() map[string][]actioncontract.NonHTTPBinding {
	bindings := make(map[string][]actioncontract.NonHTTPBinding, len(operationsDefinitionCatalog))
	for _, operation := range operationsDefinitionCatalog {
		if operationsOwnedNonHTTPActionKeys[operation.ActionKey] {
			continue
		}
		bindings[operation.ActionKey] = append(bindings[operation.ActionKey], actioncontract.NonHTTPBinding{
			Kind: operationsActionBindingKind, InvocationKey: operation.Kind,
		})
	}
	return bindings
}
