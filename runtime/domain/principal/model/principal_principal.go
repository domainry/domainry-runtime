package principalmodel

import (
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

// NewPrincipalFromIdentity creates Runtime execution context around the SDK
// principal without translating identity or authorization state. The SDK
// principal and its AccessBundle remain the only human authorization source.
func NewPrincipalFromIdentity(source identitysdk.Principal, requestID string) Principal {
	return Principal{
		Principal: source,
		RequestID: requestID,
	}
}

// PermissionKeys returns the effective functional grants exposed to Runtime
// presentation and audit boundaries. SDK bundles are authoritative and deny
// grants remove an otherwise matching allow.
func (principal Principal) PermissionKeys() []string {
	if principal.AccessBundle == nil {
		if !principal.SystemScope.Valid() {
			return nil
		}
		return append([]string(nil), principal.SystemCapabilities...)
	}
	return principal.Principal.PermissionKeys()
}

// Principal is Runtime execution context around the SDK-owned authenticated
// principal. Identity facts and authorization state are embedded directly;
// Runtime adds only process execution, business-profile, and system-authority
// context.
type Principal struct {
	identitysdk.Principal
	SystemScope           SystemScope
	RequestID             string
	CorrelationID         string
	CausationID           string
	BusinessProfiles      []profilebindingmodel.Reference
	ActiveBusinessProfile *profilebindingmodel.Reference
	BusinessClaims        map[string]profilebindingmodel.ClaimValue
	// BusinessAuthorizationRevision binds Identity authorization to the current
	// business profiles and selection without changing the SDK-owned revision.
	BusinessAuthorizationRevision string
	// AuthorizationEvaluatedAt freezes the access-bundle validation instant for
	// one admitted request or governed execution. Every policy evaluation in
	// that execution uses the same instant, so a bundle cannot expire midway
	// through an otherwise authorized transaction.
	AuthorizationEvaluatedAt time.Time `json:"-"`
	// SystemCapabilities are explicit, process-owned capabilities. They are
	// honored only when SystemScope is valid and are never populated for a
	// human, API-key, or other externally authenticated principal.
	SystemCapabilities []string `json:"-"`
	AutomationDepth    int
	VisitedRuleKeys    []string
}

func (principal Principal) WithAuthorizationEvaluationTime(at time.Time) Principal {
	if principal.AccessBundle != nil && principal.AuthorizationEvaluatedAt.IsZero() {
		principal.AuthorizationEvaluatedAt = at.UTC()
	}
	return principal
}

func (principal Principal) AuthorizationEvaluationTime() time.Time {
	if !principal.AuthorizationEvaluatedAt.IsZero() {
		return principal.AuthorizationEvaluatedAt.UTC()
	}
	return time.Now().UTC()
}

// EffectiveAuthorizationRevision invalidates business evidence when either
// Identity authorization or the selected business context changes.
func (principal Principal) EffectiveAuthorizationRevision() string {
	if principal.BusinessAuthorizationRevision != "" {
		return principal.BusinessAuthorizationRevision
	}
	return principal.AuthorizationRevision
}

// HasPermission evaluates SDK function grants for authenticated principals.
// A Runtime-owned system principal must carry an explicit valid SystemScope
// and SystemCapabilities. Runtime has no role-definition authorization
// fallback.
func (principal Principal) HasPermission(permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return false
	}
	if principal.AccessBundle != nil {
		return principal.Principal.HasPermission(permission)
	}
	if !principal.SystemScope.Valid() {
		return false
	}
	return systemCapabilityAllows(principal.SystemCapabilities, permission)
}

// HasExactPermission evaluates the same exact functional+data authorization as
// HasPermission. The separate name is retained for call sites that want to
// emphasize that wildcards and aliases are forbidden; it is not a
// function-only bypass.
func (principal Principal) HasExactPermission(permission string) bool {
	return principal.HasPermission(permission)
}

// HasAllPermissions requires every declared SDK function grant. An empty
// permission set is not authorization: authored endpoints must opt into a
// stable permission key explicitly.
func (principal Principal) HasAllPermissions(permissions []string) bool {
	if principal.AccessBundle != nil {
		return principal.Principal.HasAllPermissions(permissions)
	}
	if len(permissions) == 0 || !principal.SystemScope.Valid() {
		return false
	}
	for _, permission := range permissions {
		if !systemCapabilityAllows(principal.SystemCapabilities, strings.TrimSpace(permission)) {
			return false
		}
	}
	return true
}

// WithExactSystemCapabilities narrows a trusted Runtime system execution to
// concrete capabilities derived from the work item it is about to execute.
// Externally authenticated principals are immutable at this boundary, and
// wildcard-shaped capabilities are rejected rather than interpreted.
func (principal Principal) WithExactSystemCapabilities(capabilities ...string) Principal {
	if !principal.Known || !principal.SystemScope.Valid() || principal.AccessBundle != nil {
		return principal
	}
	exact := append([]string(nil), principal.SystemCapabilities...)
	seen := make(map[string]bool, len(exact)+len(capabilities))
	for _, capability := range exact {
		seen[strings.TrimSpace(capability)] = true
	}
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || strings.Contains(capability, "*") || seen[capability] {
			continue
		}
		seen[capability] = true
		exact = append(exact, capability)
	}
	principal.SystemCapabilities = exact
	return principal
}

func (principal Principal) Allows(objectKey, action string) bool {
	action = strings.TrimSpace(action)
	objectKey = strings.TrimSpace(objectKey)
	return objectKey != "" && action != "" && principal.HasPermission(objectKey+"."+action)
}

func systemCapabilityAllows(capabilities []string, permission string) bool {
	for _, granted := range capabilities {
		granted = strings.TrimSpace(granted)
		if granted == permission {
			return true
		}
	}
	return false
}
