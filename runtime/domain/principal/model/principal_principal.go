package principalmodel

import (
	"strings"

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
	// SystemCapabilities are explicit, process-owned capabilities. They are
	// honored only when SystemScope is valid and are never populated for a
	// human, API-key, or other externally authenticated principal.
	SystemCapabilities []string `json:"-"`
	AutomationDepth    int
	VisitedRuleKeys    []string
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

// HasExactPermission compares the canonical granted key literally. It does not
// interpret "*" or an object wildcard as an alias for another Action.
func (principal Principal) HasExactPermission(permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return false
	}
	for _, granted := range principal.PermissionKeys() {
		if strings.TrimSpace(granted) == permission {
			return true
		}
	}
	return false
}

// HasAllPermissions requires every declared SDK function grant. An empty
// permission set is not authorization: authored surfaces must opt into a
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
