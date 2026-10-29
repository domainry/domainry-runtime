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
		operationKey := operation.ActionKey[separator+1:]
		localized := runtimeOperationPermissionText(operationKey)
		label := localized["en"].Label
		definition, err := actioncontract.NormalizeDefinition(actioncontract.ActionDefinition{
			Key: operation.ActionKey, Owner: "runtime:operations", SourceKind: "runtime_operations",
			CapabilityKey: "runtime.operations", CapabilityLabel: "Runtime operations",
			OperationKey: operationKey, OperationLabel: localized["en"].OperationLabel, Label: label,
			Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
			Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
			NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: operationsActionBindingKind, InvocationKey: operation.Kind}},
			Permission: &actioncontract.PermissionDefinition{
				Key: operation.ActionKey, Owner: "runtime:operations", ModuleKey: "runtime", GroupKey: "runtime.operations",
				ResourceKey: operation.ActionKey[:separator], OperationKey: operationKey, Label: label, Category: "Runtime Operations",
				GrantMode: actioncontract.PermissionGrantInternal, ScopeKind: actioncontract.PermissionScopeNone,
				DefaultLocale: "en", I18n: localized, LifecycleStatus: actioncontract.LifecycleActive,
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

func runtimeOperationPermissionText(operationKey string) actioncontract.PermissionLocalizedText {
	english := map[string]string{
		"enable_automation_rule": "Enable automation rule", "disable_automation_rule": "Disable automation rule",
		"retry_integration_event": "Retry integration event", "replay_integration_event": "Replay integration event",
		"retry_runtime_publication": "Retry Runtime publication", "reconcile_integration_invocation": "Reconcile integration invocation",
		"create_backup": "Create backup", "restore_backup": "Restore backup", "delete_workspace": "Delete workspace",
		"enable_maintenance": "Enable maintenance mode", "disable_maintenance": "Disable maintenance mode",
		"pause_worker_owner": "Pause worker owner", "resume_worker_owner": "Resume worker owner",
		"drain_runtime_instance": "Drain Runtime instance", "undrain_runtime_instance": "Return Runtime instance to service",
		"resolve_dead_letter": "Resolve failed job", "retry_dead_letter": "Retry failed job", "acknowledge_dead_letter": "Acknowledge failed job",
	}[operationKey]
	chinese := map[string]string{
		"enable_automation_rule": "启用自动化规则", "disable_automation_rule": "停用自动化规则",
		"retry_integration_event": "重试集成事件", "replay_integration_event": "重放集成事件",
		"retry_runtime_publication": "重试 Runtime 发布", "reconcile_integration_invocation": "核对集成调用",
		"create_backup": "创建备份", "restore_backup": "恢复备份", "delete_workspace": "删除工作区",
		"enable_maintenance": "开启维护模式", "disable_maintenance": "关闭维护模式",
		"pause_worker_owner": "暂停任务所有者", "resume_worker_owner": "恢复任务所有者",
		"drain_runtime_instance": "摘除 Runtime 实例", "undrain_runtime_instance": "恢复 Runtime 实例服务",
		"resolve_dead_letter": "处理失败任务", "retry_dead_letter": "重试失败任务", "acknowledge_dead_letter": "确认失败任务",
	}[operationKey]
	return actioncontract.PermissionLocalizedText{
		"en":    {ModuleLabel: "Runtime", GroupLabel: "Runtime operations", ResourceLabel: "Runtime operations", OperationLabel: english, Label: english, Description: "Allows this high-risk Runtime operation."},
		"zh-CN": {ModuleLabel: "Runtime", GroupLabel: "Runtime 运维", ResourceLabel: "Runtime 运维操作", OperationLabel: chinese, Label: chinese, Description: "允许执行此项高风险 Runtime 运维操作。"},
	}
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
