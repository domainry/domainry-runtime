package runtime

import (
	"context"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func resolveIdentitySDKPrincipal(ctx context.Context, resolver identitysdk.PrincipalResolver, userID, roleKey string) (principalmodel.Principal, error) {
	resolution, err := resolver.Resolve(ctx, identitysdk.PrincipalResolutionRequest{
		SubjectID: identitysdk.SubjectID(strings.TrimSpace(userID)), RoleKey: strings.TrimSpace(roleKey),
	})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, ""), nil
}
