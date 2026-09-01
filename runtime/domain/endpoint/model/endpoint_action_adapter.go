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
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAnonymousProtocol, PolicyKey: "runtime.http.anonymous"}
	case strings.HasPrefix(policy, "integration_entrypoint_policy:"), len(contract.ProtocolAudiences) != 0:
		definition.Authorization = actioncontract.Authorization{
			Strategy: actioncontract.AuthorizationServiceIdentity, PolicyKey: policy,
			Audiences: append([]string(nil), contract.ProtocolAudiences...),
		}
	case strings.HasPrefix(policy, "owner_handler_policy:"):
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticatedPrincipal}
	case strings.HasPrefix(policy, "static_permission:"), policy == "audit.business_event_export_policy":
		definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission}
		separator := strings.LastIndex(definition.Key, ".")
		resourceKey := definition.Key[:separator]
		definition.Permission = &actioncontract.PermissionDefinition{
			Key: definition.Key, Owner: owner, ResourceKey: resourceKey, ActionKey: operationKey,
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
		return []actioncontract.ApprovalPolicy{actioncontract.ApprovalConfirmation}
	case HighRiskActionBreakGlass:
		return []actioncontract.ApprovalPolicy{actioncontract.ApprovalBreakGlass}
	default:
		return nil
	}
}
