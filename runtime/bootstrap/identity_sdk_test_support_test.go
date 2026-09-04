package bootstrap

import (
	"context"

	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type bootstrapIdentityBindingStub struct{ modulecapability.Binding }

func (bootstrapIdentityBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeModule}
}
func (bootstrapIdentityBindingStub) Authentication() identitysdk.Authentication {
	return bootstrapIdentityAuthenticationStub{}
}
func (bootstrapIdentityBindingStub) Tokens() identitysdk.TokenVerifier {
	return bootstrapIdentityTokenVerifierStub{}
}
func (bootstrapIdentityBindingStub) Authorization() identitysdk.Authorization {
	return bootstrapIdentityAuthorizationStub{}
}
func (bootstrapIdentityBindingStub) Principals() identitysdk.PrincipalResolver {
	return bootstrapIdentityPrincipalResolverStub{}
}
func (bootstrapIdentityBindingStub) Projection() identitysdk.Projection {
	return bootstrapIdentityProjectionStub{}
}
func (bootstrapIdentityBindingStub) Applications() identitysdk.ApplicationRegistry {
	return bootstrapIdentityApplicationRegistryStub{}
}
func (bootstrapIdentityBindingStub) Permissions() identitysdk.PermissionRegistry {
	return bootstrapIdentityPermissionRegistryStub{}
}
func (bootstrapIdentityBindingStub) Credentials() identitysdk.CredentialManager {
	return bootstrapIdentityCredentialsStub{}
}
func (bootstrapIdentityBindingStub) Close(context.Context) error { return nil }

type bootstrapIdentityAuthenticationStub struct{}

func (bootstrapIdentityAuthenticationStub) Providers(context.Context, identitysdk.ProviderQuery) ([]identitysdk.Provider, error) {
	return nil, nil
}
func (bootstrapIdentityAuthenticationStub) LoginWithPassword(context.Context, identitysdk.PasswordLoginRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (bootstrapIdentityAuthenticationStub) BeginFederatedLogin(context.Context, identitysdk.BeginFederatedLoginRequest) (identitysdk.ProviderChallenge, error) {
	return identitysdk.ProviderChallenge{}, nil
}
func (bootstrapIdentityAuthenticationStub) CompleteFederatedLogin(context.Context, identitysdk.CompleteFederatedLoginRequest) (identitysdk.FederatedLoginCompletion, error) {
	return identitysdk.FederatedLoginCompletion{}, nil
}
func (bootstrapIdentityAuthenticationStub) ExchangeAuthorizationCode(context.Context, identitysdk.ExchangeAuthorizationCodeRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (bootstrapIdentityAuthenticationStub) VerifyOTP(context.Context, identitysdk.VerifyOTPRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (bootstrapIdentityAuthenticationStub) RefreshSession(context.Context, identitysdk.RefreshRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (bootstrapIdentityAuthenticationStub) LogoutSession(context.Context, identitysdk.LogoutRequest) error {
	return nil
}
func (bootstrapIdentityAuthenticationStub) CurrentSession(context.Context, identitysdk.CurrentSessionRequest) (identitysdk.SessionView, error) {
	return identitysdk.SessionView{}, nil
}

type bootstrapIdentityTokenVerifierStub struct{}

func (bootstrapIdentityTokenVerifierStub) Verify(context.Context, identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	return identitysdk.VerifiedToken{}, nil
}

type bootstrapIdentityAuthorizationStub struct{}

func (bootstrapIdentityAuthorizationStub) ResolveAccess(context.Context, identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	return identitysdk.AccessBundle{}, nil
}
func (bootstrapIdentityAuthorizationStub) Reauthorize(context.Context, identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	return identitysdk.AccessDecision{}, nil
}

type bootstrapIdentityPrincipalResolverStub struct{}

func (bootstrapIdentityPrincipalResolverStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	return identitysdk.PrincipalResolution{}, nil
}

type bootstrapIdentityProjectionStub struct{}

func (bootstrapIdentityProjectionStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (bootstrapIdentityProjectionStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (bootstrapIdentityProjectionStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (bootstrapIdentityProjectionStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (bootstrapIdentityProjectionStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

type bootstrapIdentityApplicationRegistryStub struct{}

func (bootstrapIdentityApplicationRegistryStub) Register(_ context.Context, request identitysdk.ApplicationRegistration) (identitysdk.ApplicationRegistrationReceipt, error) {
	return identitysdk.ApplicationRegistrationReceipt{Application: request.Application, RedirectURLs: request.CanonicalRedirectURLs(), Status: "active"}, request.ValidateContract()
}

type bootstrapIdentityPermissionRegistryStub struct{}

func (bootstrapIdentityPermissionRegistryStub) CurrentSourceSnapshot(_ context.Context, request identitysdk.PermissionSourceSnapshotRequest) (identitysdk.PermissionSourceSnapshot, error) {
	if err := request.ValidateContract(); err != nil {
		return identitysdk.PermissionSourceSnapshot{}, err
	}
	return identitysdk.PermissionSourceSnapshot{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner}, nil
}

func (bootstrapIdentityPermissionRegistryStub) Reconcile(_ context.Context, request identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	return identitysdk.PermissionReconcileReceipt{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner, PreviousSnapshotHash: request.PreviousSnapshotHash, SnapshotHash: request.SnapshotHash, DefinitionCount: len(request.Definitions), Inserted: len(request.Definitions)}, request.ValidateContract()
}

type bootstrapIdentityCredentialsStub struct{}

func (bootstrapIdentityCredentialsStub) ChangePassword(context.Context, identitysdk.ChangePasswordRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (bootstrapIdentityCredentialsStub) ResetPassword(context.Context, identitysdk.ResetPasswordRequest) error {
	return nil
}
func (bootstrapIdentityCredentialsStub) RevokeSessions(context.Context, identitysdk.RevokeSessionsRequest) error {
	return nil
}

var _ identitysdk.Binding = bootstrapIdentityBindingStub{}
