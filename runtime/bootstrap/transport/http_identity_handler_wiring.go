package transport

import (
	"context"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (a *httpServerAssembly) wireIdentityReferences(constructionContext context.Context) {
	records := a.dependencies.Records
	if projection := a.dependencies.IdentityBinding.Projection(); projection != nil {
		records.Applications().AuthoringCapabilities.UseIdentityReferenceSource(constructionContext, identitySDKCapabilityReferenceSource(projection))
	}
}

func identitySDKCapabilityReferenceSource(projection identitysdk.Projection) func(context.Context, principalmodel.Principal) (capabilityapplication.CapabilityIdentityReferences, error) {
	return func(ctx context.Context, _ principalmodel.Principal) (capabilityapplication.CapabilityIdentityReferences, error) {
		users, err := projection.ListUsers(ctx, identitysdk.ProjectionQuery{})
		if err != nil {
			return capabilityapplication.CapabilityIdentityReferences{}, err
		}
		roles, err := projection.ListRoles(ctx, identitysdk.ProjectionQuery{})
		if err != nil {
			return capabilityapplication.CapabilityIdentityReferences{}, err
		}
		references := capabilityapplication.CapabilityIdentityReferences{
			UserIDs: make([]string, 0, len(users)), RoleIDs: make([]string, 0, len(roles)),
			OrgIDs: make([]string, 0, len(users)),
		}
		for _, user := range users {
			references.UserIDs = append(references.UserIDs, user.ID)
		}
		for _, role := range roles {
			references.RoleIDs = append(references.RoleIDs, role.ID)
		}
		organizationUnits := map[string]struct{}{}
		for _, user := range users {
			if value := strings.TrimSpace(user.OrgID); value != "" {
				organizationUnits[value] = struct{}{}
			}
		}
		for organizationUnitID := range organizationUnits {
			references.OrgIDs = append(references.OrgIDs, organizationUnitID)
		}
		sort.Strings(references.UserIDs)
		sort.Strings(references.RoleIDs)
		sort.Strings(references.OrgIDs)
		return references, nil
	}
}
