package endpointmodel

import (
	"fmt"
	"strings"
)

const ContractVersion = "runtime-endpoint-contract-v1"

type ListenerExposure string

const (
	ListenerExposurePublic      ListenerExposure = "public"
	ListenerExposureTenantAdmin ListenerExposure = "tenant-admin"
	ListenerExposureOps         ListenerExposure = "ops"
)

type EndpointEffectClass string

const (
	EndpointEffectRead  EndpointEffectClass = "read"
	EndpointEffectWrite EndpointEffectClass = "write"
)

type HighRiskActionPolicy string

const (
	HighRiskActionNone            HighRiskActionPolicy = "none"
	HighRiskActionReasonRequired  HighRiskActionPolicy = "reason_required"
	HighRiskActionConfirmRequired HighRiskActionPolicy = "confirmation_required"
	HighRiskActionBreakGlass      HighRiskActionPolicy = "break_glass_required"
)

// RuntimeEndpointContractV1 declares transport exposure and the server-side
// policy owned by an endpoint. It never identifies a frontend product shell.
type RuntimeEndpointContractV1 struct {
	ContractVersion     string               `json:"contract_version"`
	EndpointIdentity    string               `json:"endpoint_identity"`
	ActionKey           string               `json:"action_key"`
	SourceOwner         string               `json:"source_owner"`
	ApplicationUseCase  string               `json:"application_use_case"`
	ListenerExposures   []ListenerExposure   `json:"listener_exposures"`
	ProtocolAudiences   []string             `json:"protocol_audiences,omitempty"`
	RequiredPermissions []string             `json:"required_permissions"`
	PermissionPolicyRef string               `json:"permission_policy_ref"`
	EffectClass         EndpointEffectClass  `json:"effect_class"`
	HighRiskPolicy      HighRiskActionPolicy `json:"high_risk_action_policy"`
	IdempotencyDecision string               `json:"idempotency_decision"`
	AuditClass          string               `json:"audit_class"`
}

func (contract RuntimeEndpointContractV1) Validate() error {
	if contract.ContractVersion != ContractVersion {
		return fmt.Errorf("endpoint contract version %q is invalid", contract.ContractVersion)
	}
	if strings.TrimSpace(contract.EndpointIdentity) == "" {
		return fmt.Errorf("endpoint identity is required")
	}
	if strings.TrimSpace(contract.ActionKey) == "" || strings.TrimSpace(contract.SourceOwner) == "" || strings.TrimSpace(contract.ApplicationUseCase) == "" {
		return fmt.Errorf("endpoint contract %q requires canonical action identity", contract.EndpointIdentity)
	}
	if len(contract.ListenerExposures) == 0 {
		return fmt.Errorf("endpoint contract %q requires a listener exposure", contract.EndpointIdentity)
	}
	seenExposures := map[ListenerExposure]bool{}
	for _, exposure := range contract.ListenerExposures {
		switch exposure {
		case ListenerExposurePublic, ListenerExposureTenantAdmin, ListenerExposureOps:
		default:
			return fmt.Errorf("endpoint contract %q has invalid listener exposure %q", contract.EndpointIdentity, exposure)
		}
		if seenExposures[exposure] {
			return fmt.Errorf("endpoint contract %q has duplicate listener exposure %q", contract.EndpointIdentity, exposure)
		}
		seenExposures[exposure] = true
	}
	switch contract.EffectClass {
	case EndpointEffectRead, EndpointEffectWrite:
	default:
		return fmt.Errorf("endpoint contract %q has invalid effect %q", contract.EndpointIdentity, contract.EffectClass)
	}
	switch contract.HighRiskPolicy {
	case HighRiskActionNone, HighRiskActionReasonRequired, HighRiskActionConfirmRequired, HighRiskActionBreakGlass:
	default:
		return fmt.Errorf("endpoint contract %q has invalid high-risk policy %q", contract.EndpointIdentity, contract.HighRiskPolicy)
	}
	if strings.TrimSpace(contract.PermissionPolicyRef) == "" {
		return fmt.Errorf("endpoint contract %q requires an operation permission policy reference", contract.EndpointIdentity)
	}
	switch {
	case contract.PermissionPolicyRef == "anonymous":
		if contract.EffectClass == EndpointEffectWrite {
			return fmt.Errorf("write endpoint contract %q cannot use anonymous authorization", contract.EndpointIdentity)
		}
	case strings.HasPrefix(contract.PermissionPolicyRef, "integration_entrypoint_policy:"):
		entrypointPolicy := strings.TrimSpace(strings.TrimPrefix(contract.PermissionPolicyRef, "integration_entrypoint_policy:"))
		if len(contract.ProtocolAudiences) == 0 || entrypointPolicy == "" || !strings.Contains(entrypointPolicy, ".") {
			return fmt.Errorf("endpoint contract %q has an unbound integration entrypoint permission policy", contract.EndpointIdentity)
		}
	case strings.HasPrefix(contract.PermissionPolicyRef, "static_permission:"):
		permission := strings.TrimSpace(strings.TrimPrefix(contract.PermissionPolicyRef, "static_permission:"))
		if permission == "" || !containsString(contract.RequiredPermissions, permission) {
			return fmt.Errorf("endpoint contract %q static permission policy must declare %q", contract.EndpointIdentity, permission)
		}
	case strings.HasPrefix(contract.PermissionPolicyRef, "owner_handler_policy:"):
		ownerPolicy := strings.TrimSpace(strings.TrimPrefix(contract.PermissionPolicyRef, "owner_handler_policy:"))
		if ownerPolicy == "" || !strings.Contains(ownerPolicy, ".") {
			return fmt.Errorf("endpoint contract %q has an unbound owner permission policy", contract.EndpointIdentity)
		}
	case contract.PermissionPolicyRef == "audit.business_event_export_policy":
		if !containsString(contract.RequiredPermissions, "audit.business.read") || !containsString(contract.RequiredPermissions, "audit.business.export") {
			return fmt.Errorf("endpoint contract %q audit export policy requires read and export permissions", contract.EndpointIdentity)
		}
	default:
		return fmt.Errorf("endpoint contract %q has unsupported operation permission policy %q", contract.EndpointIdentity, contract.PermissionPolicyRef)
	}
	if contract.EffectClass == EndpointEffectWrite {
		if strings.TrimSpace(contract.IdempotencyDecision) == "" || strings.TrimSpace(contract.AuditClass) == "" {
			return fmt.Errorf("write endpoint contract %q requires idempotency and audit policy", contract.EndpointIdentity)
		}
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
