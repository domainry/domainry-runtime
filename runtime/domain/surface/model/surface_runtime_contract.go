package surfacemodel

import (
	"fmt"
	"strings"
)

type ContractSubjectKind string

const (
	ContractSubjectRoute    ContractSubjectKind = "route"
	ContractSubjectEndpoint ContractSubjectKind = "endpoint"
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

// RuntimeSurfaceContractV1 is the shared, versioned classification envelope
// used by routes, HTTP endpoints, discovery documents, and generated artifacts.
// Authorization remains a server-side Principal decision; this contract only
// declares the access facts that the server must enforce.
type RuntimeSurfaceContractV1 struct {
	ContractVersion     string               `json:"contract_version"`
	SubjectKind         ContractSubjectKind  `json:"subject_kind"`
	Identity            string               `json:"identity"`
	Surface             ProductSurface       `json:"surface"`
	Shell               ShellClass           `json:"shell,omitempty"`
	ActorAudiences      []ActorAudience      `json:"actor_audiences"`
	RequiredPermissions []string             `json:"required_permissions"`
	ExposureClass       ExposureClass        `json:"exposure_class"`
	EffectClass         EndpointEffectClass  `json:"effect_class,omitempty"`
	HighRiskPolicy      HighRiskActionPolicy `json:"high_risk_action_policy"`
}

type RuntimeEndpointSurfaceProjectionV1 struct {
	Surface       ProductSurface `json:"surface"`
	ActorAudience ActorAudience  `json:"actor_audience"`
	ExposureClass ExposureClass  `json:"exposure_class"`
}

// RuntimeEndpointContractV1 is the executable endpoint classification compiled
// from the reviewed inventory. PermissionPolicyRef names the owner policy that
// performs operation-level authorization when the permission cannot be static
// (for example dynamic Object CRUD and Action effect authorization).
type RuntimeEndpointContractV1 struct {
	ContractVersion     string                               `json:"contract_version"`
	EndpointIdentity    string                               `json:"endpoint_identity"`
	Projections         []RuntimeEndpointSurfaceProjectionV1 `json:"surface_projections"`
	ProtocolAudiences   []string                             `json:"protocol_audiences,omitempty"`
	RequiredPermissions []string                             `json:"required_permissions"`
	PermissionPolicyRef string                               `json:"permission_policy_ref"`
	EffectClass         EndpointEffectClass                  `json:"effect_class"`
	HighRiskPolicy      HighRiskActionPolicy                 `json:"high_risk_action_policy"`
	IdempotencyDecision string                               `json:"idempotency_decision"`
	AuditClass          string                               `json:"audit_class"`
}

func (contract RuntimeEndpointContractV1) Validate() error {
	if contract.ContractVersion != ContractVersion {
		return fmt.Errorf("endpoint contract version %q is invalid", contract.ContractVersion)
	}
	if strings.TrimSpace(contract.EndpointIdentity) == "" {
		return fmt.Errorf("endpoint identity is required")
	}
	if len(contract.Projections) == 0 && len(contract.ProtocolAudiences) == 0 {
		return fmt.Errorf("endpoint contract %q requires a Surface projection or protocol audience", contract.EndpointIdentity)
	}
	for _, projection := range contract.Projections {
		if !projection.Surface.Valid() {
			return fmt.Errorf("endpoint contract %q has invalid Surface %q", contract.EndpointIdentity, projection.Surface)
		}
		audience, _ := projection.Surface.RequiredAudience()
		exposure, _ := projection.Surface.Exposure()
		if projection.ActorAudience != audience || projection.ExposureClass != exposure {
			return fmt.Errorf("endpoint contract %q projection for %q does not match the backend Surface policy", contract.EndpointIdentity, projection.Surface)
		}
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

func (contract RuntimeSurfaceContractV1) Validate() error {
	if contract.ContractVersion != ContractVersion {
		return fmt.Errorf("surface contract version %q is invalid", contract.ContractVersion)
	}
	if strings.TrimSpace(contract.Identity) == "" {
		return fmt.Errorf("surface contract identity is required")
	}
	if !contract.Surface.Valid() {
		return fmt.Errorf("surface contract Surface %q is invalid", contract.Surface)
	}
	requiredAudience, _ := contract.Surface.RequiredAudience()
	if len(contract.ActorAudiences) == 0 || !containsAudience(contract.ActorAudiences, requiredAudience) {
		return fmt.Errorf("surface contract %q must include audience %q", contract.Identity, requiredAudience)
	}
	expectedExposure, _ := contract.Surface.Exposure()
	if contract.ExposureClass != expectedExposure {
		return fmt.Errorf("surface contract %q must use exposure %q", contract.Identity, expectedExposure)
	}
	switch contract.HighRiskPolicy {
	case HighRiskActionNone, HighRiskActionReasonRequired, HighRiskActionConfirmRequired, HighRiskActionBreakGlass:
	default:
		return fmt.Errorf("surface contract %q has invalid high-risk policy %q", contract.Identity, contract.HighRiskPolicy)
	}
	switch contract.SubjectKind {
	case ContractSubjectRoute:
		expectedShell, _ := contract.Surface.Shell()
		if contract.Shell != expectedShell {
			return fmt.Errorf("route contract %q must use shell %q", contract.Identity, expectedShell)
		}
		if contract.EffectClass != "" {
			return fmt.Errorf("route contract %q must not declare an endpoint effect", contract.Identity)
		}
	case ContractSubjectEndpoint:
		if contract.Shell != "" {
			return fmt.Errorf("endpoint contract %q must not declare a shell", contract.Identity)
		}
		if contract.EffectClass != EndpointEffectRead && contract.EffectClass != EndpointEffectWrite {
			return fmt.Errorf("endpoint contract %q has invalid effect %q", contract.Identity, contract.EffectClass)
		}
		if contract.EffectClass == EndpointEffectWrite && len(trimmedPermissionKeys(contract.RequiredPermissions)) == 0 {
			return fmt.Errorf("write endpoint contract %q requires an operation permission", contract.Identity)
		}
	default:
		return fmt.Errorf("surface contract %q has invalid subject kind %q", contract.Identity, contract.SubjectKind)
	}
	return nil
}

func containsAudience(values []ActorAudience, expected ActorAudience) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func trimmedPermissionKeys(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
