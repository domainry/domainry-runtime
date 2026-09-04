package workflow

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

// workflowIdentityProjectionTestStub supplies the SDK projection methods a focused test
// does not care about. Individual test doubles override only the projection
// calls exercised by that scenario.
type workflowIdentityProjectionTestStub struct{}

func (workflowIdentityProjectionTestStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}

func (workflowIdentityProjectionTestStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}

func (workflowIdentityProjectionTestStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}

func (workflowIdentityProjectionTestStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (workflowIdentityProjectionTestStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

type workflowPrincipalResolverTestStub struct {
	resolution identitysdk.PrincipalResolution
	err        error
	request    identitysdk.PrincipalResolutionRequest
}

func (s *workflowPrincipalResolverTestStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	s.request = request
	return s.resolution, s.err
}

func workflowPrincipalResolution(principal principalmodel.Principal) identitysdk.PrincipalResolution {
	bundle := identitysdk.AccessBundle{}
	if principal.AccessBundle != nil {
		bundle = *principal.AccessBundle
	}
	sdkPrincipal := principal.Principal
	sdkPrincipal.AccessBundle = nil
	return identitysdk.PrincipalResolution{Principal: sdkPrincipal, AccessBundle: bundle}
}

func workflowPrincipalWithPermissions(principal principalmodel.Principal, permissions ...string) principalmodel.Principal {
	role := accessfixture.FromPrincipal(principal)
	role.Permissions = append([]string(nil), permissions...)
	return accessfixture.Attach(principal, role)
}
