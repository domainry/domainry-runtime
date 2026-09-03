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
		Key: contract.ActionKey, Owner: owner, SourceKind: "builtin_surface",
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
		case ListenerExposureTenantAdmin:
			definition.Exposures = append(definition.Exposures, actioncontract.ExposureTenantAdmin)
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
			Strategy: actioncontract.AuthorizationSigned, PolicyKey: policy,
			Audiences: append([]string(nil), contract.ProtocolAudiences...),
		}
	case strings.HasPrefix(policy, "owner_handler_policy:"):
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}
	case strings.HasPrefix(policy, "static_permission:"):
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}
		separator := strings.LastIndex(definition.Key, ".")
		resourceKey := definition.Key[:separator]
		definition.Permission = &actioncontract.PermissionDefinition{
			Key: definition.Key, Owner: owner, ResourceKey: resourceKey, OperationKey: operationKey,
			Label: operationLabel, Category: strings.TrimSpace(contract.SourceOwner), LifecycleStatus: actioncontract.LifecycleActive,
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
		case ListenerExposureTenantAdmin:
			want[actioncontract.ExposureTenantAdmin] = true
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
