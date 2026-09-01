package runtime

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type runtimeIdentityBindingStub struct{ modulecapability.Binding }

func (runtimeIdentityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeModule}
}
func (runtimeIdentityBindingStub) Authentication() identitysdk.Authentication {
	return runtimeIdentityAuthenticationStub{}
}
func (runtimeIdentityBindingStub) Tokens() identitysdk.TokenVerifier {
	return runtimeIdentityTokenVerifierStub{}
}
func (runtimeIdentityBindingStub) Authorization() identitysdk.Authorization {
	return runtimeIdentityAuthorizationStub{}
}
func (runtimeIdentityBindingStub) Principals() identitysdk.PrincipalResolver {
	return runtimeIdentityPrincipalResolverStub{}
}
func (runtimeIdentityBindingStub) Directory() identitysdk.Directory {
	return runtimeIdentityDirectoryStub{}
}
func (runtimeIdentityBindingStub) Catalog() identitysdk.CatalogClient {
	return runtimeIdentityCatalogStub{}
}
func (runtimeIdentityBindingStub) Credentials() identitysdk.CredentialManager {
	return runtimeIdentityCredentialsStub{}
}
func (runtimeIdentityBindingStub) Close(context.Context) error { return nil }

type runtimeIdentityAuthenticationStub struct{}

func (runtimeIdentityAuthenticationStub) Providers(context.Context, identitysdk.ProviderQuery) ([]identitysdk.Provider, error) {
	return nil, nil
}
func (runtimeIdentityAuthenticationStub) LoginWithPassword(context.Context, identitysdk.PasswordLoginRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (runtimeIdentityAuthenticationStub) BeginFederatedLogin(context.Context, identitysdk.BeginFederatedLoginRequest) (identitysdk.ProviderChallenge, error) {
	return identitysdk.ProviderChallenge{}, nil
}
func (runtimeIdentityAuthenticationStub) CompleteFederatedLogin(context.Context, identitysdk.CompleteFederatedLoginRequest) (identitysdk.FederatedLoginCompletion, error) {
	return identitysdk.FederatedLoginCompletion{}, nil
}
func (runtimeIdentityAuthenticationStub) ExchangeAuthorizationCode(context.Context, identitysdk.ExchangeAuthorizationCodeRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (runtimeIdentityAuthenticationStub) VerifyOTP(context.Context, identitysdk.VerifyOTPRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (runtimeIdentityAuthenticationStub) RefreshSession(context.Context, identitysdk.RefreshRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (runtimeIdentityAuthenticationStub) LogoutSession(context.Context, identitysdk.LogoutRequest) error {
	return nil
}
func (runtimeIdentityAuthenticationStub) CurrentSession(context.Context, identitysdk.CurrentSessionRequest) (identitysdk.SessionView, error) {
	return identitysdk.SessionView{}, nil
}

type runtimeIdentityTokenVerifierStub struct{}

func (runtimeIdentityTokenVerifierStub) Verify(context.Context, identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	return identitysdk.VerifiedToken{}, nil
}

type runtimeIdentityAuthorizationStub struct{}

func (runtimeIdentityAuthorizationStub) ResolveAccess(context.Context, identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	return identitysdk.AccessBundle{}, nil
}
func (runtimeIdentityAuthorizationStub) Reauthorize(context.Context, identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	return identitysdk.AccessDecision{}, nil
}

type runtimeIdentityPrincipalResolverStub struct{}

func (runtimeIdentityPrincipalResolverStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	roleKey := request.RoleKey
	if roleKey == "" {
		roleKey = "admin"
	}
	principal := identitysdk.Principal{
		Known: true, WorkspaceID: string(request.Application.WorkspaceID), UserID: string(request.SubjectID), RoleKey: roleKey,
		Permissions: []string{"workspace.admin"}, AuthorizationRevision: "test-authorization",
	}
	bundle := identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion, CatalogRevision: "runtime-test-catalog", AuthorizationRevision: "test-authorization", ExpiresAt: time.Now().Add(time.Hour),
		Subject:        identitysdk.Subject{WorkspaceID: request.Application.WorkspaceID, SubjectID: request.SubjectID},
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: "workspace", Action: "admin", Effect: identitysdk.EffectAllow}},
	}
	principal.AccessBundle = &bundle
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

type runtimeIdentityDirectoryStub struct{}

func (runtimeIdentityDirectoryStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (runtimeIdentityDirectoryStub) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}
func (runtimeIdentityDirectoryStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (runtimeIdentityDirectoryStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (runtimeIdentityDirectoryStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}
func (runtimeIdentityDirectoryStub) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

type runtimeIdentityCatalogStub struct{}

func (runtimeIdentityCatalogStub) Validate(context.Context, identitysdk.AuthorizationCatalog) error {
	return nil
}
func (runtimeIdentityCatalogStub) Publish(context.Context, identitysdk.AuthorizationCatalog) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}
func (runtimeIdentityCatalogStub) CurrentRevision(context.Context, identitysdk.ApplicationRef) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{}, nil
}

type runtimeIdentityCredentialsStub struct{}

func (runtimeIdentityCredentialsStub) ChangePassword(context.Context, identitysdk.ChangePasswordRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (runtimeIdentityCredentialsStub) ResetPassword(context.Context, identitysdk.ResetPasswordRequest) error {
	return nil
}
func (runtimeIdentityCredentialsStub) RevokeSessions(context.Context, identitysdk.RevokeSessionsRequest) error {
	return nil
}

var _ identitysdk.Binding = runtimeIdentityBindingStub{}
