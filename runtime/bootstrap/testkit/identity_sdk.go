package testkit

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// IdentityBindingStub is a non-authoritative SDK contract fixture for HTTP
// assembly tests whose exercised routes are public. It exists so tests still
// satisfy Runtime's required Identity dependency without importing an
// Identity module implementation or making the production dependency
// optional.
type IdentityBindingStub struct{}

func (IdentityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{ProtocolVersion: identitysdk.CurrentProtocolVersion, BundleVersion: identitysdk.CurrentPolicyBundleVersion, CatalogVersion: identitysdk.CatalogVersionV1, Mode: identitysdk.DeploymentModeModule}
}
func (IdentityBindingStub) Authentication() identitysdk.Authentication {
	return identityAuthenticationStub{}
}
func (IdentityBindingStub) Tokens() identitysdk.TokenVerifier { return identityTokenVerifierStub{} }
func (IdentityBindingStub) Authorization() identitysdk.Authorization {
	return identityAuthorizationStub{}
}
func (IdentityBindingStub) Principals() identitysdk.PrincipalResolver {
	return identityPrincipalResolverStub{}
}
func (IdentityBindingStub) Directory() identitysdk.Directory   { return identityDirectoryStub{} }
func (IdentityBindingStub) Catalog() identitysdk.CatalogClient { return identityCatalogStub{} }
func (IdentityBindingStub) Credentials() identitysdk.CredentialManager {
	return identityCredentialsStub{}
}
func (IdentityBindingStub) Close(context.Context) error { return nil }

type identityAuthenticationStub struct{}

func (identityAuthenticationStub) Providers(context.Context, identitysdk.ProviderQuery) ([]identitysdk.Provider, error) {
	return nil, nil
}
func (identityAuthenticationStub) LoginWithPassword(context.Context, identitysdk.PasswordLoginRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (identityAuthenticationStub) BeginFederatedLogin(context.Context, identitysdk.BeginFederatedLoginRequest) (identitysdk.ProviderChallenge, error) {
	return identitysdk.ProviderChallenge{}, nil
}
func (identityAuthenticationStub) CompleteFederatedLogin(context.Context, identitysdk.CompleteFederatedLoginRequest) (identitysdk.FederatedLoginCompletion, error) {
	return identitysdk.FederatedLoginCompletion{}, nil
}
func (identityAuthenticationStub) ExchangeAuthorizationCode(context.Context, identitysdk.ExchangeAuthorizationCodeRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (identityAuthenticationStub) VerifyOTP(context.Context, identitysdk.VerifyOTPRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (identityAuthenticationStub) RefreshSession(context.Context, identitysdk.RefreshRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (identityAuthenticationStub) LogoutSession(context.Context, identitysdk.LogoutRequest) error {
	return nil
}
func (identityAuthenticationStub) CurrentSession(context.Context, identitysdk.CurrentSessionRequest) (identitysdk.SessionView, error) {
	return identitysdk.SessionView{}, nil
}

type identityTokenVerifierStub struct{}

func (identityTokenVerifierStub) Verify(context.Context, identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	return identitysdk.VerifiedToken{}, nil
}

type identityAuthorizationStub struct{}

func (identityAuthorizationStub) ResolveAccess(context.Context, identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	return identitysdk.AccessBundle{}, nil
}
func (identityAuthorizationStub) Reauthorize(context.Context, identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	return identitysdk.AccessDecision{}, nil
}

type identityPrincipalResolverStub struct{}

func (identityPrincipalResolverStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	return identitysdk.PrincipalResolution{}, nil
}

type identityDirectoryStub struct{}

func (identityDirectoryStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (identityDirectoryStub) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}
func (identityDirectoryStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (identityDirectoryStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (identityDirectoryStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}
func (identityDirectoryStub) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

type identityCatalogStub struct{}

func (identityCatalogStub) Validate(context.Context, identitysdk.AuthorizationCatalog) error {
	return nil
}
func (identityCatalogStub) Publish(context.Context, identitysdk.AuthorizationCatalog) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}
func (identityCatalogStub) CurrentRevision(context.Context, identitysdk.ApplicationRef) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}

type identityCredentialsStub struct{}

func (identityCredentialsStub) ChangePassword(context.Context, identitysdk.ChangePasswordRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (identityCredentialsStub) ResetPassword(context.Context, identitysdk.ResetPasswordRequest) error {
	return nil
}
func (identityCredentialsStub) RevokeSessions(context.Context, identitysdk.RevokeSessionsRequest) error {
	return nil
}

var _ identitysdk.Binding = IdentityBindingStub{}
