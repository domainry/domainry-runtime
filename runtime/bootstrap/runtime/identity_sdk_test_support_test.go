package runtime

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/requestcontext"
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
func (runtimeIdentityBindingStub) Projection() identitysdk.Projection {
	return runtimeIdentityProjectionStub{}
}
func (runtimeIdentityBindingStub) Applications() identitysdk.ApplicationRegistry {
	return runtimeIdentityApplicationRegistryStub{}
}
func (runtimeIdentityBindingStub) Permissions() identitysdk.PermissionRegistry {
	return runtimeIdentityPermissionRegistryStub{}
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

func (runtimeIdentityPrincipalResolverStub) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	roleKey := request.RoleKey
	if roleKey == "" {
		roleKey = "admin"
	}
	workspaceID := requestcontext.WorkspaceID(ctx)
	if workspaceID == "" {
		workspaceID = "workspace-primary"
	}
	principal := identitysdk.Principal{
		Known: true, WorkspaceID: workspaceID, UserID: string(request.SubjectID), RoleKey: roleKey,
		Permissions: []string{"runtime.appschema.validate_application_definition"}, AuthorizationRevision: "test-authorization",
	}
	bundle := identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion, AuthorizationRevision: "test-authorization", ExpiresAt: time.Now().Add(time.Hour),
		Subject:        identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(workspaceID), SubjectID: request.SubjectID},
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: "workspace", Action: "admin", Effect: identitysdk.EffectAllow}},
	}
	principal.AccessBundle = &bundle
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

type runtimeIdentityProjectionStub struct{}

func (runtimeIdentityProjectionStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (runtimeIdentityProjectionStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (runtimeIdentityProjectionStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (runtimeIdentityProjectionStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (runtimeIdentityProjectionStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

type runtimeIdentityApplicationRegistryStub struct{}

func (runtimeIdentityApplicationRegistryStub) Register(_ context.Context, request identitysdk.ApplicationRegistration) (identitysdk.ApplicationRegistrationReceipt, error) {
	return identitysdk.ApplicationRegistrationReceipt{Application: request.Application, RedirectURLs: request.RedirectURLs, Status: "active"}, nil
}

type runtimeIdentityPermissionRegistryStub struct{}

func (runtimeIdentityPermissionRegistryStub) CurrentSourceSnapshot(_ context.Context, request identitysdk.PermissionSourceSnapshotRequest) (identitysdk.PermissionSourceSnapshot, error) {
	return identitysdk.PermissionSourceSnapshot{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner}, nil
}

func (runtimeIdentityPermissionRegistryStub) Reconcile(_ context.Context, request identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	return identitysdk.PermissionReconcileReceipt{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner, PreviousSnapshotHash: request.PreviousSnapshotHash, SnapshotHash: request.SnapshotHash, DefinitionCount: len(request.Definitions), Inserted: len(request.Definitions)}, request.ValidateContract()
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
