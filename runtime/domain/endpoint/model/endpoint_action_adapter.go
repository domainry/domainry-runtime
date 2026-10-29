package endpointmodel

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

// AuthorizationActionDefinition projects a generator-owned endpoint contract
// into the shared Action authorization contract without duplicating handler
// pointers or endpoint policy strings.
func AuthorizationActionDefinition(contract RuntimeEndpointContractV1) (actioncontract.ActionDefinition, error) {
	if err := contract.Validate(); err != nil {
		return actioncontract.ActionDefinition{}, err
	}
	method, routeTemplate, found := strings.Cut(strings.TrimSpace(contract.EndpointIdentity), " ")
	if !found {
		return actioncontract.ActionDefinition{}, fmt.Errorf("endpoint contract %q has no HTTP method/path binding", contract.EndpointIdentity)
	}
	owner := "runtime:" + strings.TrimSpace(contract.SourceOwner)
	operationKey := contract.ActionKey[strings.LastIndex(contract.ActionKey, ".")+1:]
	operationLabel := strings.TrimSpace(contract.ApplicationUseCase)
	definition := actioncontract.ActionDefinition{
		Key: contract.ActionKey, Owner: owner, SourceKind: "builtin_http",
		CapabilityKey: "runtime." + strings.TrimSpace(contract.SourceOwner), CapabilityLabel: strings.TrimSpace(contract.SourceOwner),
		OperationKey: operationKey, OperationLabel: operationLabel, Label: operationLabel,
		Authorization: actioncontract.Authorization{},
		HTTP:          &actioncontract.HTTPBinding{Method: method, RouteTemplate: routeTemplate, DisplayRouteTemplate: endpointDisplayRoute(routeTemplate)},
		EffectClass:   actioncontract.EffectClass(contract.EffectClass), RiskLevel: endpointRiskLevel(contract),
		ApprovalPolicies:    endpointApprovalPolicies(contract.HighRiskPolicy),
		IdempotencyDecision: strings.TrimSpace(contract.IdempotencyDecision), AuditClass: strings.TrimSpace(contract.AuditClass), LifecycleStatus: actioncontract.LifecycleActive,
	}
	for _, exposure := range contract.ListenerExposures {
		switch exposure {
		case ListenerExposurePublic:
			definition.Exposures = append(definition.Exposures, actioncontract.ExposurePublic)
		case ListenerExposureManagement:
			definition.Exposures = append(definition.Exposures, actioncontract.ExposureManagement)
		case ListenerExposureOps:
			definition.Exposures = append(definition.Exposures, actioncontract.ExposureOps)
		}
	}
	policy := strings.TrimSpace(contract.PermissionPolicyRef)
	switch {
	case policy == "anonymous":
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAnonymous}
	case strings.HasPrefix(policy, "integration_entrypoint_policy:"), len(contract.ProtocolAudiences) != 0:
		definition.Authorization = actioncontract.Authorization{
			Strategy:  actioncontract.AuthorizationSigned,
			PolicyKey: strings.TrimSpace(strings.TrimPrefix(policy, "integration_entrypoint_policy:")),
			Audiences: append([]string(nil), contract.ProtocolAudiences...),
		}
	case strings.HasPrefix(policy, "owner_handler_policy:"):
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}
	case strings.HasPrefix(policy, "static_permission:"):
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}
		separator := strings.LastIndex(definition.Key, ".")
		resourceKey := definition.Key[:separator]
		grantMode := actioncontract.PermissionGrantInternal
		if strings.TrimSpace(contract.SourceOwner) == "automation" {
			grantMode = actioncontract.PermissionGrantDirect
		} else if strings.TrimSpace(contract.SourceOwner) == "workflows" {
			grantMode = actioncontract.PermissionGrantBundled
		}
		localized := runtimeEndpointPermissionText(contract.SourceOwner, resourceKey, operationKey)
		operationLabel = localized["en"].Label
		definition.OperationLabel, definition.Label = localized["en"].OperationLabel, operationLabel
		definition.Permission = &actioncontract.PermissionDefinition{
			Key: definition.Key, Owner: owner, ModuleKey: "runtime", GroupKey: "runtime." + strings.TrimSpace(contract.SourceOwner),
			ResourceKey: resourceKey, OperationKey: operationKey, Label: operationLabel, Category: strings.TrimSpace(contract.SourceOwner),
			GrantMode: grantMode, ScopeKind: actioncontract.PermissionScopeNone,
			DefaultLocale: "en", I18n: localized, LifecycleStatus: actioncontract.LifecycleActive,
		}
	default:
		return actioncontract.ActionDefinition{}, fmt.Errorf("endpoint contract %q has no Action authorization adapter for %q", contract.EndpointIdentity, policy)
	}
	normalized, err := actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return actioncontract.ActionDefinition{}, fmt.Errorf("Runtime endpoint %q Action projection: %w", contract.EndpointIdentity, err)
	}
	return normalized, nil
}

func runtimeEndpointPermissionText(sourceOwner, resourceKey, operationKey string) actioncontract.PermissionLocalizedText {
	groupEnglish := map[string]string{
		"appschema": "Application schema", "automation": "Automation", "businessevents": "Business events", "discovery": "Runtime discovery",
		"dispatch": "Dispatch", "notifications": "Runtime notifications", "operations": "Runtime operations", "publicationhandoff": "Publication handoff",
		"records": "Business records", "root": "Runtime status", "uploads": "File uploads", "workflows": "Workflows", "workspaceprovision": "Workspace administration",
	}[strings.TrimSpace(sourceOwner)]
	groupChinese := map[string]string{
		"appschema": "应用模型", "automation": "自动化", "businessevents": "业务事件", "discovery": "Runtime 发现",
		"dispatch": "任务分发", "notifications": "Runtime 通知", "operations": "Runtime 运维", "publicationhandoff": "发布交接",
		"records": "业务数据", "root": "Runtime 状态", "uploads": "文件上传", "workflows": "工作流", "workspaceprovision": "工作区管理",
	}[strings.TrimSpace(sourceOwner)]
	resourceEnglish := map[string]string{
		"runtime.appschema": "Application schema diagnostics", "runtime.notifications": "Notification deliveries",
		"runtime.operations": "Runtime operations", "runtime.records": "Business profiles",
		"runtime.workspaceprovision": "Workspaces",
	}[resourceKey]
	resourceChinese := map[string]string{
		"runtime.appschema": "应用模型诊断", "runtime.notifications": "通知投递记录",
		"runtime.operations": "Runtime 运维操作", "runtime.records": "业务档案",
		"runtime.workspaceprovision": "工作区",
	}[resourceKey]
	if resourceEnglish == "" {
		resourceEnglish = groupEnglish
	}
	if resourceChinese == "" {
		resourceChinese = groupChinese
	}
	operationEnglish := map[string]string{
		"ops_metadata_diagnostics": "View metadata diagnostics", "list_deliveries": "List deliveries",
		"advance_database_retirement": "Advance database retirement", "apply_bulk_dead_letters": "Apply bulk failed-job action",
		"capture_diagnostics": "Capture diagnostics", "disable_break_glass": "Disable break-glass access",
		"discover_database_retirement": "Discover database retirement", "dry_run_bulk_dead_letters": "Preview bulk failed-job action",
		"enable_break_glass": "Enable break-glass access", "execute_database_retirement": "Execute database retirement",
		"force_release_lease": "Force release lease", "get_database_retirement": "View database retirement",
		"get_operation": "View operation", "inspect_dead_letter": "Inspect failed job", "list_break_glass": "List break-glass access",
		"list_controls": "List operational controls", "list_database_retirements": "List database retirements",
		"list_operations": "List operations", "operation_catalog": "View operation catalog", "operation_runbook": "View operation runbook",
		"preview_database_retirement": "Preview database retirement", "receipts": "View operation receipts", "reset": "Reset operation",
		"retry": "Retry operation", "deactivate_business_profile": "Deactivate business profile",
		"reactivate_business_profile": "Reactivate business profile", "list_workspaces": "List workspaces",
		"provision_workspace": "Provision workspace", "reactivate_workspace": "Reactivate workspace", "suspend_workspace": "Suspend workspace",
		"update_commercial_configuration": "Update commercial configuration",
	}[operationKey]
	operationChinese := map[string]string{
		"ops_metadata_diagnostics": "查看元数据诊断", "list_deliveries": "查看投递记录",
		"advance_database_retirement": "推进数据库退役", "apply_bulk_dead_letters": "执行批量失败任务操作",
		"capture_diagnostics": "采集诊断信息", "disable_break_glass": "关闭紧急访问",
		"discover_database_retirement": "发现待退役数据库", "dry_run_bulk_dead_letters": "预演批量失败任务操作",
		"enable_break_glass": "开启紧急访问", "execute_database_retirement": "执行数据库退役",
		"force_release_lease": "强制释放租约", "get_database_retirement": "查看数据库退役任务",
		"get_operation": "查看运维任务", "inspect_dead_letter": "检查失败任务", "list_break_glass": "查看紧急访问",
		"list_controls": "查看运维控制项", "list_database_retirements": "查看数据库退役任务",
		"list_operations": "查看运维任务", "operation_catalog": "查看操作目录", "operation_runbook": "查看操作手册",
		"preview_database_retirement": "预览数据库退役", "receipts": "查看操作回执", "reset": "重置操作",
		"retry": "重试操作", "deactivate_business_profile": "停用业务档案",
		"reactivate_business_profile": "重新启用业务档案", "list_workspaces": "查看工作区",
		"provision_workspace": "创建工作区", "reactivate_workspace": "重新启用工作区", "suspend_workspace": "暂停工作区",
		"update_commercial_configuration": "更新商业配置",
	}[operationKey]
	if operationEnglish == "" {
		operationEnglish = strings.ReplaceAll(operationKey, "_", " ")
	}
	if operationChinese == "" {
		operationChinese = "执行操作"
	}
	return actioncontract.PermissionLocalizedText{
		"en":    {ModuleLabel: "Runtime", GroupLabel: groupEnglish, ResourceLabel: resourceEnglish, OperationLabel: operationEnglish, Label: resourceEnglish + " · " + operationEnglish, Description: "Provides access to " + resourceEnglish + " for the “" + operationEnglish + "” operation."},
		"zh-CN": {ModuleLabel: "Runtime", GroupLabel: groupChinese, ResourceLabel: resourceChinese, OperationLabel: operationChinese, Label: resourceChinese + " · " + operationChinese, Description: "允许执行“" + operationChinese + "”。"},
	}
}

// ValidateHostFacadeAction proves that a module-owned Action has one real
// Runtime-hosted handler with matching execution governance. The generated
// endpoint's legacy ActionKey and permission strings are deliberately ignored:
// the contributed module manifest is the only authorization vocabulary.
func ValidateHostFacadeAction(contract RuntimeEndpointContractV1, definition actioncontract.ActionDefinition) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	normalized, err := actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return err
	}
	owner := strings.TrimSpace(contract.SourceOwner)
	if normalized.SourceKind != "host_facade" || normalized.Owner != "module:"+owner {
		return fmt.Errorf("Action %q is not a host facade owned by module:%s", normalized.Key, owner)
	}
	if normalized.HTTP == nil || normalized.HTTP.Method+" "+normalized.HTTP.RouteTemplate != strings.TrimSpace(contract.EndpointIdentity) {
		return fmt.Errorf("Action %q does not bind Runtime handler %q", normalized.Key, contract.EndpointIdentity)
	}
	if !strings.HasPrefix(strings.TrimSpace(contract.PermissionPolicyRef), "static_permission:") ||
		normalized.Authorization.Strategy != actioncontract.AuthorizationAuthenticated ||
		normalized.Permission == nil || normalized.Permission.Key != normalized.Key || normalized.Permission.Owner != normalized.Owner {
		return fmt.Errorf("Action %q is not an exact same-key role Action", normalized.Key)
	}
	if normalized.EffectClass != actioncontract.EffectClass(contract.EffectClass) {
		return fmt.Errorf("Action %q effect %q differs from Runtime handler effect %q", normalized.Key, normalized.EffectClass, contract.EffectClass)
	}
	if normalized.RiskLevel != endpointRiskLevel(contract) {
		return fmt.Errorf("Action %q risk %q differs from Runtime handler risk %q", normalized.Key, normalized.RiskLevel, endpointRiskLevel(contract))
	}
	if strings.TrimSpace(normalized.IdempotencyDecision) != strings.TrimSpace(contract.IdempotencyDecision) {
		return fmt.Errorf("Action %q idempotency %q differs from Runtime handler idempotency %q", normalized.Key, normalized.IdempotencyDecision, contract.IdempotencyDecision)
	}
	if !sameEndpointExposures(normalized.Exposures, contract.ListenerExposures) {
		return fmt.Errorf("Action %q listener exposures differ from Runtime handler %q", normalized.Key, contract.EndpointIdentity)
	}
	if !sameApprovalPolicies(normalized.ApprovalPolicies, endpointApprovalPolicies(contract.HighRiskPolicy)) {
		return fmt.Errorf("Action %q approval policies differ from Runtime handler %q", normalized.Key, contract.EndpointIdentity)
	}
	return nil
}

func sameEndpointExposures(actual []actioncontract.Exposure, expected []ListenerExposure) bool {
	want := make(map[actioncontract.Exposure]bool, len(expected))
	for _, exposure := range expected {
		switch exposure {
		case ListenerExposurePublic:
			want[actioncontract.ExposurePublic] = true
		case ListenerExposureManagement:
			want[actioncontract.ExposureManagement] = true
		case ListenerExposureOps:
			want[actioncontract.ExposureOps] = true
		default:
			return false
		}
	}
	if len(actual) != len(want) {
		return false
	}
	for _, exposure := range actual {
		if !want[exposure] {
			return false
		}
	}
	return true
}

func sameApprovalPolicies(actual, expected []actioncontract.ApprovalPolicy) bool {
	want := make(map[actioncontract.ApprovalPolicy]bool, len(expected))
	for _, policy := range expected {
		want[policy] = true
	}
	if len(actual) != len(want) {
		return false
	}
	for _, policy := range actual {
		if !want[policy] {
			return false
		}
	}
	return true
}

func endpointDisplayRoute(routeTemplate string) string {
	if strings.Contains(routeTemplate, "{objectKey}") || strings.Contains(routeTemplate, "{actionKey}") {
		return ""
	}
	return routeTemplate
}

func endpointRiskLevel(contract RuntimeEndpointContractV1) actioncontract.RiskLevel {
	switch contract.HighRiskPolicy {
	case HighRiskActionBreakGlass:
		return actioncontract.RiskCritical
	case HighRiskActionReasonRequired, HighRiskActionConfirmRequired:
		return actioncontract.RiskHigh
	}
	if contract.EffectClass == EndpointEffectWrite {
		return actioncontract.RiskMedium
	}
	return actioncontract.RiskLow
}

func endpointApprovalPolicies(policy HighRiskActionPolicy) []actioncontract.ApprovalPolicy {
	switch policy {
	case HighRiskActionReasonRequired:
		return []actioncontract.ApprovalPolicy{actioncontract.ApprovalReason}
	case HighRiskActionConfirmRequired:
		return []actioncontract.ApprovalPolicy{actioncontract.ApprovalReason, actioncontract.ApprovalConfirmation}
	case HighRiskActionBreakGlass:
		return []actioncontract.ApprovalPolicy{actioncontract.ApprovalReason, actioncontract.ApprovalBreakGlass}
	default:
		return nil
	}
}
