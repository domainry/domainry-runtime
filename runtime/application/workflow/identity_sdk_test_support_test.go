package workflow

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

// workflowDirectoryTestStub supplies the SDK Directory methods a focused test
// does not care about. Individual test doubles override only the projection
// calls exercised by that scenario.
type workflowDirectoryTestStub struct{}

func (workflowDirectoryTestStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}

func (workflowDirectoryTestStub) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}

func (workflowDirectoryTestStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}

func (workflowDirectoryTestStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (workflowDirectoryTestStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func (workflowDirectoryTestStub) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
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
