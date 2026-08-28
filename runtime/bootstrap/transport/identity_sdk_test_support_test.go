package transport

import (
	"context"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type transportIdentityBindingStub struct{}

func (transportIdentityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeModule}
}
func (transportIdentityBindingStub) Authentication() identitysdk.Authentication {
	return transportIdentityAuthenticationStub{}
}
func (transportIdentityBindingStub) Tokens() identitysdk.TokenVerifier {
	return transportIdentityTokenVerifierStub{}
}
func (transportIdentityBindingStub) Authorization() identitysdk.Authorization {
	return transportIdentityAuthorizationStub{}
}
func (transportIdentityBindingStub) Principals() identitysdk.PrincipalResolver {
	return transportIdentityPrincipalResolverStub{}
}
func (transportIdentityBindingStub) Directory() identitysdk.Directory {
	return transportIdentityDirectoryStub{}
}
func (transportIdentityBindingStub) Catalog() identitysdk.CatalogClient {
	return transportIdentityCatalogStub{}
}
func (transportIdentityBindingStub) Credentials() identitysdk.CredentialManager {
	return transportIdentityCredentialsStub{}
}
func (transportIdentityBindingStub) Close(context.Context) error { return nil }

type transportIdentityAuthenticationStub struct{}

func (transportIdentityAuthenticationStub) Providers(context.Context, identitysdk.ProviderQuery) ([]identitysdk.Provider, error) {
	return nil, nil
}
func (transportIdentityAuthenticationStub) LoginWithPassword(context.Context, identitysdk.PasswordLoginRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (transportIdentityAuthenticationStub) BeginFederatedLogin(context.Context, identitysdk.BeginFederatedLoginRequest) (identitysdk.ProviderChallenge, error) {
	return identitysdk.ProviderChallenge{}, nil
}
func (transportIdentityAuthenticationStub) CompleteFederatedLogin(context.Context, identitysdk.CompleteFederatedLoginRequest) (identitysdk.FederatedLoginCompletion, error) {
	return identitysdk.FederatedLoginCompletion{}, nil
}
func (transportIdentityAuthenticationStub) ExchangeAuthorizationCode(context.Context, identitysdk.ExchangeAuthorizationCodeRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (transportIdentityAuthenticationStub) VerifyOTP(context.Context, identitysdk.VerifyOTPRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (transportIdentityAuthenticationStub) RefreshSession(context.Context, identitysdk.RefreshRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (transportIdentityAuthenticationStub) LogoutSession(context.Context, identitysdk.LogoutRequest) error {
	return nil
}
func (transportIdentityAuthenticationStub) CurrentSession(context.Context, identitysdk.CurrentSessionRequest) (identitysdk.SessionView, error) {
	return identitysdk.SessionView{}, nil
}

type transportIdentityTokenVerifierStub struct{}

func (transportIdentityTokenVerifierStub) Verify(context.Context, identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	return identitysdk.VerifiedToken{}, nil
}

type transportIdentityAuthorizationStub struct{}

func (transportIdentityAuthorizationStub) ResolveAccess(context.Context, identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	return identitysdk.AccessBundle{}, nil
}
func (transportIdentityAuthorizationStub) Reauthorize(context.Context, identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	return identitysdk.AccessDecision{}, nil
}

type transportIdentityPrincipalResolverStub struct{}

func (transportIdentityPrincipalResolverStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	roleKey := strings.TrimSpace(request.RoleKey)
	if roleKey == "" {
		roleKey = "admin"
	}
	permissions := map[string][]string{
		"admin": {"workspace.admin", "identity.users.read"}, "operator": {"operations.read"}, "business": {"customer.read"},
	}[roleKey]
	bundle := identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion, Subject: identitysdk.Subject{
		SubjectID: request.SubjectID, WorkspaceID: request.Application.WorkspaceID,
	}}
	for _, permission := range permissions {
		resource, action, _ := strings.Cut(permission, ".")
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
	}
	principal := identitysdk.Principal{Known: len(permissions) > 0, UserID: string(request.SubjectID), WorkspaceID: string(request.Application.WorkspaceID), RoleKey: roleKey, AccessBundle: &bundle}
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

type transportIdentityDirectoryStub struct{}

func (transportIdentityDirectoryStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (transportIdentityDirectoryStub) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}
func (transportIdentityDirectoryStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (transportIdentityDirectoryStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (transportIdentityDirectoryStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}
func (transportIdentityDirectoryStub) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

type transportIdentityCatalogStub struct{}

func (transportIdentityCatalogStub) Validate(context.Context, identitysdk.AuthorizationCatalog) error {
	return nil
}
func (transportIdentityCatalogStub) Publish(context.Context, identitysdk.AuthorizationCatalog) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}
func (transportIdentityCatalogStub) CurrentRevision(context.Context, identitysdk.ApplicationRef) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}

type transportIdentityCredentialsStub struct{}

func (transportIdentityCredentialsStub) ChangePassword(context.Context, identitysdk.ChangePasswordRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (transportIdentityCredentialsStub) ResetPassword(context.Context, identitysdk.ResetPasswordRequest) error {
	return nil
}
func (transportIdentityCredentialsStub) RevokeSessions(context.Context, identitysdk.RevokeSessionsRequest) error {
	return nil
}

var _ identitysdk.Binding = transportIdentityBindingStub{}
