package transport

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type transportIdentityBindingStub struct{ identitysdk.Binding }

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
func (transportIdentityBindingStub) Projection() identitysdk.Projection {
	return transportIdentityProjectionStub{}
}
func (transportIdentityBindingStub) Applications() identitysdk.ApplicationRegistry {
	return transportIdentityApplicationRegistryStub{}
}
func (transportIdentityBindingStub) Permissions() identitysdk.PermissionRegistry {
	return transportIdentityPermissionRegistryStub{}
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

func (transportIdentityPrincipalResolverStub) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	roleKey := strings.TrimSpace(request.RoleKey)
	if roleKey == "" {
		roleKey = "admin"
	}
	permissions := map[string][]string{
		"admin": {"runtime.appschema.validate_application_definition", "identity.users.read"}, "operator": {"runtime.operations.list_operations", "runtime.operations.get_operation"}, "business": {"customer.read"},
	}[roleKey]
	workspaceID := requestcontext.WorkspaceID(ctx)
	if workspaceID == "" {
		workspaceID = "workspace-primary"
	}
	bundle := identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion, Subject: identitysdk.Subject{
		SubjectID: request.SubjectID, WorkspaceID: identitysdk.WorkspaceID(workspaceID),
	}}
	for _, permission := range permissions {
		resource, action, _ := strings.Cut(permission, ".")
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
	}
	principal := identitysdk.Principal{Known: len(permissions) > 0, UserID: string(request.SubjectID), WorkspaceID: workspaceID, RoleKey: roleKey, AccessBundle: &bundle}
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

type transportIdentityProjectionStub struct{}

func (transportIdentityProjectionStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (transportIdentityProjectionStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (transportIdentityProjectionStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (transportIdentityProjectionStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (transportIdentityProjectionStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

type transportIdentityApplicationRegistryStub struct{}

func (transportIdentityApplicationRegistryStub) Register(_ context.Context, request identitysdk.ApplicationRegistration) (identitysdk.ApplicationRegistrationReceipt, error) {
	return identitysdk.ApplicationRegistrationReceipt{Application: request.Application, RedirectURLs: request.CanonicalRedirectURLs(), Status: "active"}, request.ValidateContract()
}

type transportIdentityPermissionRegistryStub struct{}

func (transportIdentityPermissionRegistryStub) Reconcile(_ context.Context, request identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	return identitysdk.PermissionReconcileReceipt{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner, PreviousSnapshotHash: request.PreviousSnapshotHash, SnapshotHash: request.SnapshotHash, DefinitionCount: len(request.Definitions), Inserted: len(request.Definitions)}, request.ValidateContract()
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
