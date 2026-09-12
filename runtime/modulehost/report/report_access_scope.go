package reportmodulehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

// ReportAccessScopeHash is a Runtime host-adapter fact. It binds every
// identity, organization, business-profile, and authorization revision value
// that can change the authorized source projection returned to Report.
func ReportAccessScopeHash(principal principalmodel.Principal) (string, error) {
	principal = canonicalReportAccessPrincipal(principal)
	payload := struct {
		WorkspaceID           string
		UserID                string
		OrgID                 string
		OrgScopeIDs           []string
		ReportingScopeUserIDs []string
		RoleKey               string
		BusinessProfiles      []profilebindingmodel.Reference
		ActiveBusinessProfile *profilebindingmodel.Reference
		BusinessClaims        map[string]profilebindingmodel.ClaimValue
		AuthorizationRevision string
		SystemScopeKind       principalmodel.SystemScopeKind
		SystemCapabilities    []string
	}{principal.WorkspaceID, principal.UserID, principal.OrgID, principal.OrgScopeIDs, principal.ReportingScopeUserIDs, principal.RoleKey, principal.BusinessProfiles, principal.ActiveBusinessProfile, principal.BusinessClaims, principal.EffectiveAuthorizationRevision(), principal.SystemScope.Kind, principal.SystemCapabilities}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalReportAccessPrincipal(principal principalmodel.Principal) principalmodel.Principal {
	principal.OrgScopeIDs = canonicalReportAccessSlice(append([]string(nil), principal.OrgScopeIDs...))
	principal.ReportingScopeUserIDs = canonicalReportAccessSlice(append([]string(nil), principal.ReportingScopeUserIDs...))
	principal.SystemCapabilities = canonicalReportAccessSlice(append([]string(nil), principal.SystemCapabilities...))
	principal.BusinessProfiles = canonicalReportAccessSlice(append([]profilebindingmodel.Reference(nil), principal.BusinessProfiles...))
	if principal.ActiveBusinessProfile != nil {
		active := *principal.ActiveBusinessProfile
		principal.ActiveBusinessProfile = &active
	}
	return principal
}

func canonicalReportAccessSlice[T any](values []T) []T {
	out := append([]T{}, values...)
	sort.Slice(out, func(left, right int) bool {
		leftJSON, _ := json.Marshal(out[left])
		rightJSON, _ := json.Marshal(out[right])
		return string(leftJSON) < string(rightJSON)
	})
	return out
}
