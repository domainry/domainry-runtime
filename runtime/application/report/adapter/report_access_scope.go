package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

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
		DepartmentID          string
		DepartmentPath        string
		ReportingPath         string
		ReportingUserIDs      []string
		TeamIDs               []string
		StoreIDs              []string
		TerritoryIDs          []string
		WarehouseIDs          []string
		RoleKey               string
		BusinessProfiles      []profilebindingmodel.Reference
		ActiveBusinessProfile *profilebindingmodel.Reference
		BusinessClaims        map[string]profilebindingmodel.ClaimValue
		AuthorizationRevision string
	}{principal.WorkspaceID, principal.UserID, principal.DepartmentID, principal.DepartmentPath, principal.ReportingPath, principal.ReportingUserIDs, principal.OrganizationScopes.TeamIDs, principal.OrganizationScopes.StoreIDs, principal.OrganizationScopes.TerritoryIDs, principal.OrganizationScopes.WarehouseIDs, principal.RoleKey, principal.BusinessProfiles, principal.ActiveBusinessProfile, principal.BusinessClaims, principal.AuthorizationRevision}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalReportAccessPrincipal(principal principalmodel.Principal) principalmodel.Principal {
	principal.ReportingUserIDs = canonicalReportAccessStrings(principal.ReportingUserIDs)
	principal.OrganizationScopes.TeamIDs = canonicalReportAccessStrings(principal.OrganizationScopes.TeamIDs)
	principal.OrganizationScopes.StoreIDs = canonicalReportAccessStrings(principal.OrganizationScopes.StoreIDs)
	principal.OrganizationScopes.TerritoryIDs = canonicalReportAccessStrings(principal.OrganizationScopes.TerritoryIDs)
	principal.OrganizationScopes.WarehouseIDs = canonicalReportAccessStrings(principal.OrganizationScopes.WarehouseIDs)
	principal.BusinessProfiles = canonicalReportAccessSlice(append([]profilebindingmodel.Reference(nil), principal.BusinessProfiles...))
	if principal.ActiveBusinessProfile != nil {
		active := *principal.ActiveBusinessProfile
		principal.ActiveBusinessProfile = &active
	}
	return principal
}

func canonicalReportAccessStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
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
