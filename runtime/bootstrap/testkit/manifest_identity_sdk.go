package testkit

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

const identityFixtureTokenPrefix = "plane-testkit-token-v1."

// IdentityFixtureAccessToken returns a deterministic bearer token issued by
// the Identity test fixture. Integration tests use this instead of Runtime
// development headers so the real SDK token/principal middleware is covered.
func IdentityFixtureAccessToken(subject, role string) string {
	encode := func(value string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(value)))
	}
	return identityFixtureTokenPrefix + encode(subject) + "." + encode(role)
}

type IdentityFixtureRole struct {
	Key                  string
	Name                 string
	Permissions          []string
	Audience             string
	AssignmentMode       string
	AllowAllBusinessData bool
	DataPredicate        *identitysdk.Predicate
	DataPolicies         []identitysdk.DataPolicy
	FieldPolicies        []identitysdk.FieldPolicy
	ExportPolicies       []identitysdk.ExportPolicy
	Guardrails           []identitysdk.Guardrail
}

type IdentityFixtureConfig struct {
	Roles               []IdentityFixtureRole
	Users               []identitysdk.User
	UserRoleAssignments map[string][]string
}

// IdentityFactory is an SDK acceptance fixture. Its state is supplied
// explicitly as Identity-owned test data; it never derives users, roles, or
// grants from the Runtime Manifest.
type IdentityFactory struct {
	roles     map[string]IdentityFixtureRole
	users     map[string]identitysdk.User
	userRoles map[string][]string
}

func NewIdentityFactory(config IdentityFixtureConfig) identitysdk.Factory {
	roles := make(map[string]IdentityFixtureRole, len(config.Roles))
	for _, role := range config.Roles {
		role.Key = strings.TrimSpace(role.Key)
		if role.Key != "" {
			roles[role.Key] = role
		}
	}
	users := map[string]identitysdk.User{}
	for _, user := range config.Users {
		id := strings.TrimSpace(string(user.ID))
		if id == "" {
			continue
		}
		if strings.TrimSpace(user.Status) == "" {
			user.Status = "active"
		}
		users[id] = user
	}
	userRoles := make(map[string][]string, len(config.UserRoleAssignments))
	for userID, roleKeys := range config.UserRoleAssignments {
		userRoles[strings.TrimSpace(userID)] = append([]string(nil), roleKeys...)
	}
	return IdentityFactory{roles: roles, users: users, userRoles: userRoles}
}

func NewDefaultIdentityFactory() identitysdk.Factory {
	return NewIdentityFactory(IdentityFixtureConfig{
		Roles: []IdentityFixtureRole{{Key: "admin", Name: "Administrator", Permissions: defaultIdentityFixturePermissions(), AllowAllBusinessData: true}},
		Users: []identitysdk.User{
			{ID: "admin", Name: "Administrator", Status: "active"},
			{ID: "runtime_fixture_user", Name: "Runtime fixture user", Email: "runtime-fixture@example.com", Status: "active"},
		},
		UserRoleAssignments: map[string][]string{"admin": {"admin"}, "runtime_fixture_user": {"admin"}},
	})
}

func defaultIdentityFixturePermissions() []string {
	permissions := []string{}
	for _, contract := range endpointmodel.EndpointContracts {
		definition, err := endpointmodel.AuthorizationActionDefinition(contract)
		if err == nil && definition.Permission != nil {
			permissions = append(permissions, definition.Permission.Key)
		}
	}
	sort.Strings(permissions)
	result := permissions[:0]
	for _, permission := range permissions {
		if len(result) == 0 || result[len(result)-1] != permission {
			result = append(result, permission)
		}
	}
	return append([]string(nil), result...)
}

func (factory IdentityFactory) Open(_ context.Context, application identitysdk.ApplicationRef) (identitysdk.Binding, error) {
	if !application.WorkspaceID.Valid() || !application.ApplicationKey.Valid() {
		return nil, &identitysdk.Error{Code: "identity.application_scope_invalid"}
	}
	return &manifestIdentityBinding{
		application: application, roles: factory.roles, users: factory.users, userRoles: factory.userRoles,
		sessions: map[string]manifestIdentitySession{}, workflowWorkloads: map[string]identitysdk.WorkflowWorkloadBinding{},
	}, nil
}

type manifestIdentitySession struct {
	subject string
	role    string
}

type manifestIdentityBinding struct {
	modulecapability.Binding
	mu                       sync.RWMutex
	application              identitysdk.ApplicationRef
	roles                    map[string]IdentityFixtureRole
	users                    map[string]identitysdk.User
	userRoles                map[string][]string
	sessions                 map[string]manifestIdentitySession
	permissions              map[string]map[string]identitysdk.PermissionDefinition
	permissionSnapshotHashes map[string]string
	workflowWorkloads        map[string]identitysdk.WorkflowWorkloadBinding
}

func (binding *manifestIdentityBinding) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{ProtocolVersion: identitysdk.CurrentProtocolVersion, BundleVersion: identitysdk.CurrentPolicyBundleVersion, AuthorizationVersion: identitysdk.CurrentAuthorizationContractVersion, Mode: identitysdk.DeploymentModeModule, Issuer: "plane-testkit-identity", Audience: string(binding.application.ApplicationKey)}
}
func (binding *manifestIdentityBinding) Authentication() identitysdk.Authentication { return binding }
func (binding *manifestIdentityBinding) Tokens() identitysdk.TokenVerifier          { return binding }
func (binding *manifestIdentityBinding) Authorization() identitysdk.Authorization   { return binding }
func (binding *manifestIdentityBinding) Principals() identitysdk.PrincipalResolver  { return binding }
func (binding *manifestIdentityBinding) Projection() identitysdk.Projection         { return binding }
func (binding *manifestIdentityBinding) Applications() identitysdk.ApplicationRegistry {
	return binding
}
func (binding *manifestIdentityBinding) Permissions() identitysdk.PermissionRegistry { return binding }
func (binding *manifestIdentityBinding) Credentials() identitysdk.CredentialManager  { return binding }
func (binding *manifestIdentityBinding) WorkflowWorkloads() identitysdk.WorkflowWorkloadIdentity {
	return binding
}
func (binding *manifestIdentityBinding) Close(context.Context) error { return nil }

func (binding *manifestIdentityBinding) Providers(context.Context, identitysdk.ProviderQuery) ([]identitysdk.Provider, error) {
	return nil, nil
}

func (binding *manifestIdentityBinding) LoginWithPassword(_ context.Context, request identitysdk.PasswordLoginRequest) (identitysdk.AuthSession, error) {
	subject := binding.findSubject(request.Login)
	if subject == "" {
		return identitysdk.AuthSession{}, &identitysdk.Error{Code: "auth.credentials_invalid", StatusCode: 401}
	}
	role := binding.firstUserRole(subject)
	if role == "" {
		return identitysdk.AuthSession{}, &identitysdk.Error{Code: "identity.role_not_found", StatusCode: 403}
	}
	token := fmt.Sprintf("plane-testkit-token-%d", time.Now().UnixNano())
	binding.mu.Lock()
	binding.sessions[token] = manifestIdentitySession{subject: subject, role: role}
	binding.mu.Unlock()
	return binding.authSession(token, subject, role), nil
}

func (binding *manifestIdentityBinding) BeginFederatedLogin(context.Context, identitysdk.BeginFederatedLoginRequest) (identitysdk.ProviderChallenge, error) {
	return identitysdk.ProviderChallenge{}, nil
}
func (binding *manifestIdentityBinding) CompleteFederatedLogin(context.Context, identitysdk.CompleteFederatedLoginRequest) (identitysdk.FederatedLoginCompletion, error) {
	return identitysdk.FederatedLoginCompletion{}, nil
}
func (binding *manifestIdentityBinding) ExchangeAuthorizationCode(context.Context, identitysdk.ExchangeAuthorizationCodeRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (binding *manifestIdentityBinding) VerifyOTP(context.Context, identitysdk.VerifyOTPRequest) (identitysdk.AuthSession, error) {
	return identitysdk.AuthSession{}, nil
}
func (binding *manifestIdentityBinding) RefreshSession(_ context.Context, request identitysdk.RefreshRequest) (identitysdk.AuthSession, error) {
	session, found := binding.session(request.RefreshToken)
	if !found {
		return identitysdk.AuthSession{}, &identitysdk.Error{Code: "auth.token_invalid", StatusCode: 401}
	}
	return binding.authSession(request.RefreshToken, session.subject, session.role), nil
}
func (binding *manifestIdentityBinding) LogoutSession(_ context.Context, request identitysdk.LogoutRequest) error {
	binding.mu.Lock()
	delete(binding.sessions, request.RefreshToken)
	binding.mu.Unlock()
	return nil
}
func (binding *manifestIdentityBinding) CurrentSession(_ context.Context, request identitysdk.CurrentSessionRequest) (identitysdk.SessionView, error) {
	session, found := binding.session(request.AccessToken)
	if !found {
		return identitysdk.SessionView{}, &identitysdk.Error{Code: "auth.token_invalid", StatusCode: 401}
	}
	role := binding.roles[session.role]
	return identitysdk.SessionView{
		SessionID: "plane-testkit-session", WorkspaceID: binding.application.WorkspaceID, SubjectID: identitysdk.SubjectID(session.subject), AuthorizationRevision: "plane-testkit-authorization",
		User: binding.users[session.subject], Roles: []identitysdk.Role{{ID: role.Key, Key: role.Key, Label: role.Name, Status: "active"}}, DefaultRole: role.Key, Permissions: append([]string(nil), role.Permissions...),
	}, nil
}

func (binding *manifestIdentityBinding) Verify(_ context.Context, request identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	session, found := binding.session(request.AccessToken)
	if !found {
		return identitysdk.VerifiedToken{}, &identitysdk.Error{Code: "auth.token_invalid", StatusCode: 401}
	}
	now := time.Now()
	return identitysdk.VerifiedToken{
		Issuer: "plane-testkit-identity", Audience: binding.application.ApplicationKey, SubjectID: identitysdk.SubjectID(session.subject), TenantID: binding.application.TenantID, WorkspaceID: binding.application.WorkspaceID,
		SessionID: "plane-testkit-session", AuthorizationRevision: "plane-testkit-authorization", IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(time.Hour).Unix(), TokenID: request.AccessToken,
	}, nil
}

func (binding *manifestIdentityBinding) ResolveAccess(_ context.Context, request identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	roleKey := request.Identity.Principal.RoleKey
	if session, found := binding.session(request.Identity.AccessToken); found {
		roleKey = session.role
	}
	return binding.accessBundle(request.Identity.Principal.UserID, roleKey), nil
}

func (binding *manifestIdentityBinding) Reauthorize(ctx context.Context, request identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	bundle, err := binding.ResolveAccess(ctx, identitysdk.AccessBundleRequest{Identity: request.Identity})
	if err != nil {
		return identitysdk.AccessDecision{}, err
	}
	decision, evaluationErr := identityevaluator.Evaluate(bundle, request.Access, request.Facts, time.Now())
	if evaluationErr != nil {
		return identitysdk.AccessDecision{}, evaluationErr
	}
	return identitysdk.AccessDecision{
		UserID: request.Identity.Principal.UserID, ObjectKey: request.Access.ObjectKey, Action: request.Access.Action, FieldKey: request.Access.FieldKey, RecordID: request.Access.RecordID,
		Allowed: decision.Allowed, AuthorizationRevision: "plane-testkit-authorization", Reason: identitysdk.AccessReason{Code: decision.Code},
	}, nil
}

func (binding *manifestIdentityBinding) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	if request.Workload != nil {
		return binding.resolveWorkflowWorkload(request)
	}
	roleKey := strings.TrimSpace(request.RoleKey)
	if roleKey == "" {
		roleKey = binding.firstUserRole(string(request.SubjectID))
	}
	role, found := binding.roles[roleKey]
	if !found {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.role_not_found", StatusCode: 403}
	}
	subject := strings.TrimSpace(string(request.SubjectID))
	if subject == "" {
		subject = "admin"
	}
	bundle := binding.accessBundle(subject, roleKey)
	principal := identitysdk.Principal{
		ContractVersion: identitysdk.PrincipalContextContractVersion, Known: true, WorkspaceID: string(binding.application.WorkspaceID), UserID: subject, RoleKey: roleKey,
		AuthorizationRevision: "plane-testkit-authorization", User: binding.users[subject], Roles: []identitysdk.Role{{ID: role.Key, Key: role.Key, Label: role.Name, Status: "active"}},
		Permissions: append([]string(nil), role.Permissions...), AccessBundle: &bundle,
	}
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

func (binding *manifestIdentityBinding) ApplyWorkflowWorkloadBindings(_ context.Context, request identitysdk.ApplyWorkflowWorkloadBindingsRequest) (identitysdk.ApplyWorkflowWorkloadBindingsResult, error) {
	if err := request.Validate(); err != nil {
		return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, err
	}
	if !binding.matchesApplication(request.Application) {
		return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, &identitysdk.Error{Code: "identity.application_scope_mismatch", StatusCode: http.StatusForbidden}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	next := make(map[string]identitysdk.WorkflowWorkloadBinding, len(request.Bindings))
	result := identitysdk.ApplyWorkflowWorkloadBindingsResult{Bindings: make([]identitysdk.WorkflowWorkloadBinding, 0, len(request.Bindings))}
	for _, spec := range request.Bindings {
		role, found := binding.roles[spec.RoleKey]
		if !found {
			return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, &identitysdk.Error{Code: "identity.workflow_workload_role_not_found", StatusCode: http.StatusNotFound}
		}
		if strings.TrimSpace(role.Audience) != "service" {
			return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, &identitysdk.Error{Code: "identity.workflow_workload_role_audience_invalid", StatusCode: http.StatusForbidden}
		}
		if strings.TrimSpace(role.AssignmentMode) != "system_managed" {
			return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, &identitysdk.Error{Code: "identity.workflow_workload_role_assignment_mode_invalid", StatusCode: http.StatusForbidden}
		}
		allowed := make(map[string]struct{}, len(role.Permissions))
		for _, permission := range role.Permissions {
			allowed[strings.TrimSpace(permission)] = struct{}{}
		}
		for _, actionKey := range spec.ActionKeys {
			if _, ok := allowed[actionKey]; !ok {
				return identitysdk.ApplyWorkflowWorkloadBindingsResult{}, &identitysdk.Error{Code: "identity.workflow_workload_action_denied", StatusCode: http.StatusForbidden}
			}
		}
		workload := identitysdk.WorkflowWorkloadBinding{
			Application: request.Application, SubjectID: identitysdk.WorkflowWorkloadSubjectID(spec.WorkflowKey),
			WorkflowKey: spec.WorkflowKey, DefinitionVersionID: spec.DefinitionVersionID, DefinitionVersion: spec.DefinitionVersion,
			RoleKey: spec.RoleKey, ActionKeys: append([]string(nil), spec.ActionKeys...), ReleaseID: request.ReleaseID, ReleaseDigest: request.ReleaseDigest,
			SourceKind: "deployment_control_plane", SourceID: string(request.Application.ApplicationKey), Status: identitysdk.WorkflowWorkloadBindingActive,
			CreatedAt: now, UpdatedAt: now,
		}
		next[spec.WorkflowKey] = workload
		result.Bindings = append(result.Bindings, workload)
	}
	binding.mu.Lock()
	binding.workflowWorkloads = next
	binding.mu.Unlock()
	return result, nil
}

func (binding *manifestIdentityBinding) GetWorkflowWorkloadBinding(_ context.Context, request identitysdk.GetWorkflowWorkloadBindingRequest) (identitysdk.WorkflowWorkloadBinding, error) {
	if err := request.Validate(); err != nil {
		return identitysdk.WorkflowWorkloadBinding{}, err
	}
	if !binding.matchesApplication(request.Application) {
		return identitysdk.WorkflowWorkloadBinding{}, &identitysdk.Error{Code: "identity.application_scope_mismatch", StatusCode: http.StatusForbidden}
	}
	binding.mu.RLock()
	workload, found := binding.workflowWorkloads[strings.TrimSpace(request.WorkflowKey)]
	binding.mu.RUnlock()
	if !found || workload.Status != identitysdk.WorkflowWorkloadBindingActive {
		return identitysdk.WorkflowWorkloadBinding{}, &identitysdk.Error{Code: "identity.workflow_workload_not_found", StatusCode: http.StatusNotFound}
	}
	if workload.DefinitionVersionID != strings.TrimSpace(request.DefinitionVersionID) {
		return identitysdk.WorkflowWorkloadBinding{}, &identitysdk.Error{Code: "identity.workflow_workload_version_mismatch", StatusCode: http.StatusForbidden}
	}
	if workload.ReleaseDigest != strings.TrimSpace(request.ReleaseDigest) {
		return identitysdk.WorkflowWorkloadBinding{}, &identitysdk.Error{Code: "identity.workflow_workload_release_digest_mismatch", StatusCode: http.StatusForbidden}
	}
	return workload, nil
}

func (binding *manifestIdentityBinding) resolveWorkflowWorkload(request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	workload := request.Workload
	workflowKey := strings.TrimSpace(workload.WorkflowKey)
	subjectID := identitysdk.WorkflowWorkloadSubjectID(workflowKey)
	if !binding.matchesApplication(request.Application) || subjectID == "" || request.SubjectID != subjectID || strings.TrimSpace(request.RoleKey) == "" || strings.TrimSpace(workload.DefinitionVersionID) == "" || workload.DefinitionVersion <= 0 || strings.TrimSpace(workload.ReleaseID) == "" || strings.TrimSpace(workload.ReleaseDigest) == "" {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_resolution_invalid", StatusCode: http.StatusBadRequest}
	}
	binding.mu.RLock()
	current, found := binding.workflowWorkloads[workflowKey]
	binding.mu.RUnlock()
	if !found || current.Status != identitysdk.WorkflowWorkloadBindingActive {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_not_found", StatusCode: http.StatusNotFound}
	}
	if current.RoleKey != strings.TrimSpace(request.RoleKey) {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_role_mismatch", StatusCode: http.StatusForbidden}
	}
	if current.DefinitionVersionID != strings.TrimSpace(workload.DefinitionVersionID) || current.DefinitionVersion != workload.DefinitionVersion {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_version_mismatch", StatusCode: http.StatusForbidden}
	}
	if current.ReleaseID != strings.TrimSpace(workload.ReleaseID) || current.ReleaseDigest != strings.TrimSpace(workload.ReleaseDigest) {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_release_digest_mismatch", StatusCode: http.StatusForbidden}
	}
	role, found := binding.roles[current.RoleKey]
	if !found || strings.TrimSpace(role.Audience) != "service" || strings.TrimSpace(role.AssignmentMode) != "system_managed" {
		return identitysdk.PrincipalResolution{}, &identitysdk.Error{Code: "identity.workflow_workload_role_unavailable", StatusCode: http.StatusForbidden}
	}
	bundle := binding.accessBundle(string(subjectID), current.RoleKey)
	principal := identitysdk.Principal{
		ContractVersion: identitysdk.PrincipalContextContractVersion, Known: true, WorkspaceID: string(binding.application.WorkspaceID), UserID: string(subjectID), RoleKey: current.RoleKey,
		AuthorizationRevision: "plane-testkit-workload-authorization", User: identitysdk.User{ID: string(subjectID), Name: workflowKey, Status: "active"},
		Roles: []identitysdk.Role{{ID: current.RoleKey, Key: current.RoleKey, Label: role.Name, Status: "active"}}, Permissions: append([]string(nil), role.Permissions...), AccessBundle: &bundle,
		Workload: &identitysdk.WorkflowWorkloadPrincipalContext{
			WorkflowKey: workflowKey, DefinitionVersionID: workload.DefinitionVersionID, DefinitionVersion: workload.DefinitionVersion,
			ReleaseID: workload.ReleaseID, ReleaseDigest: workload.ReleaseDigest, TaskID: strings.TrimSpace(workload.TaskID), SourceEventID: strings.TrimSpace(workload.SourceEventID), InitiatorSubjectID: workload.InitiatorSubjectID,
		},
	}
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

func (binding *manifestIdentityBinding) matchesApplication(application identitysdk.ApplicationScope) bool {
	if application.WorkspaceID != binding.application.WorkspaceID || application.ApplicationKey != binding.application.ApplicationKey {
		return false
	}
	return application.TenantID == "" || binding.application.TenantID == "" || application.TenantID == binding.application.TenantID
}

func (binding *manifestIdentityBinding) FindUser(_ context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	user, found := binding.users[string(lookup.UserID)]
	return user, found, nil
}
func (binding *manifestIdentityBinding) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (binding *manifestIdentityBinding) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	keys := make([]string, 0, len(binding.users))
	for key := range binding.users {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	users := make([]identitysdk.User, 0, len(keys))
	for _, key := range keys {
		users = append(users, binding.users[key])
	}
	return users, nil
}
func (binding *manifestIdentityBinding) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	keys := make([]string, 0, len(binding.roles))
	for key := range binding.roles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	roles := make([]identitysdk.Role, 0, len(keys))
	for _, key := range keys {
		role := binding.roles[key]
		roles = append(roles, identitysdk.Role{ID: role.Key, Key: role.Key, Label: role.Name, Status: "active"})
	}
	return roles, nil
}
func (binding *manifestIdentityBinding) ListUserRoleAssignments(_ context.Context, query identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	assignments := []identitysdk.UserRoleAssignment{}
	for userID, roleKeys := range binding.userRoles {
		if query.UserID != "" && userID != string(query.UserID) {
			continue
		}
		for _, roleKey := range roleKeys {
			assignments = append(assignments, identitysdk.UserRoleAssignment{UserID: userID, RoleID: roleKey, Status: "active", Source: "identity_test_fixture"})
		}
	}
	return assignments, nil
}
func (binding *manifestIdentityBinding) Register(_ context.Context, request identitysdk.ApplicationRegistration) (identitysdk.ApplicationRegistrationReceipt, error) {
	if err := request.ValidateContract(); err != nil {
		return identitysdk.ApplicationRegistrationReceipt{}, err
	}
	return identitysdk.ApplicationRegistrationReceipt{Application: request.Application, RedirectURLs: request.CanonicalRedirectURLs(), Status: "active", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func (binding *manifestIdentityBinding) Reconcile(_ context.Context, request identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	if err := request.ValidateContract(); err != nil {
		return identitysdk.PermissionReconcileReceipt{}, err
	}
	definitions := make(map[string]identitysdk.PermissionDefinition, len(request.Definitions))
	for _, definition := range request.Definitions {
		definitions[definition.PermissionKey] = definition
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.permissions == nil {
		binding.permissions = map[string]map[string]identitysdk.PermissionDefinition{}
	}
	if binding.permissionSnapshotHashes == nil {
		binding.permissionSnapshotHashes = map[string]string{}
	}
	currentSnapshotHash := binding.permissionSnapshotHashes[request.SourceOwner]
	if request.PreviousSnapshotHash != currentSnapshotHash && request.SnapshotHash != currentSnapshotHash {
		return identitysdk.PermissionReconcileReceipt{}, &identitysdk.Error{Code: "identity.permission_reconcile_stale_snapshot", StatusCode: 409}
	}
	binding.permissions[request.SourceOwner] = definitions
	binding.permissionSnapshotHashes[request.SourceOwner] = request.SnapshotHash
	return identitysdk.PermissionReconcileReceipt{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner, PreviousSnapshotHash: currentSnapshotHash, SnapshotHash: request.SnapshotHash, DefinitionCount: len(definitions), Updated: len(definitions)}, nil
}

func (binding *manifestIdentityBinding) CurrentSourceSnapshot(_ context.Context, request identitysdk.PermissionSourceSnapshotRequest) (identitysdk.PermissionSourceSnapshot, error) {
	if err := request.ValidateContract(); err != nil {
		return identitysdk.PermissionSourceSnapshot{}, err
	}
	binding.mu.RLock()
	definitionsByKey := binding.permissions[request.SourceOwner]
	definitions := make([]identitysdk.PermissionDefinition, 0, len(definitionsByKey))
	for _, definition := range definitionsByKey {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].PermissionKey < definitions[right].PermissionKey })
	snapshotHash := binding.permissionSnapshotHashes[request.SourceOwner]
	binding.mu.RUnlock()
	return identitysdk.PermissionSourceSnapshot{
		WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner,
		SnapshotHash: snapshotHash, Definitions: definitions,
	}, nil
}

func (binding *manifestIdentityBinding) ChangePassword(_ context.Context, request identitysdk.ChangePasswordRequest) (identitysdk.AuthSession, error) {
	session, found := binding.session(request.AccessToken)
	if !found {
		return identitysdk.AuthSession{}, &identitysdk.Error{Code: "auth.token_invalid", StatusCode: 401}
	}
	return binding.authSession(request.AccessToken, session.subject, session.role), nil
}
func (binding *manifestIdentityBinding) ResetPassword(context.Context, identitysdk.ResetPasswordRequest) error {
	return nil
}
func (binding *manifestIdentityBinding) RevokeSessions(context.Context, identitysdk.RevokeSessionsRequest) error {
	return nil
}

func (binding *manifestIdentityBinding) findSubject(login string) string {
	login = strings.TrimSpace(login)
	if _, found := binding.users[login]; found {
		return login
	}
	for id, user := range binding.users {
		if strings.EqualFold(strings.TrimSpace(user.Email), login) {
			return id
		}
	}
	return ""
}

func (binding *manifestIdentityBinding) firstUserRole(subject string) string {
	for _, roleKey := range binding.userRoles[strings.TrimSpace(subject)] {
		if _, found := binding.roles[roleKey]; found {
			return roleKey
		}
	}
	return ""
}

func (binding *manifestIdentityBinding) session(token string) (manifestIdentitySession, bool) {
	binding.mu.RLock()
	session, found := binding.sessions[strings.TrimSpace(token)]
	binding.mu.RUnlock()
	if found {
		return session, true
	}
	return binding.fixtureTokenSession(token)
}

func (binding *manifestIdentityBinding) fixtureTokenSession(token string) (manifestIdentitySession, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, identityFixtureTokenPrefix) {
		return manifestIdentitySession{}, false
	}
	parts := strings.Split(strings.TrimPrefix(token, identityFixtureTokenPrefix), ".")
	if len(parts) != 2 {
		return manifestIdentitySession{}, false
	}
	decode := func(value string) (string, bool) {
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || strings.TrimSpace(string(raw)) == "" {
			return "", false
		}
		return strings.TrimSpace(string(raw)), true
	}
	subject, subjectOK := decode(parts[0])
	roleKey, roleOK := decode(parts[1])
	if !subjectOK || !roleOK {
		return manifestIdentitySession{}, false
	}
	if _, exists := binding.roles[roleKey]; !exists {
		return manifestIdentitySession{}, false
	}
	return manifestIdentitySession{subject: subject, role: roleKey}, true
}

func (binding *manifestIdentityBinding) authSession(token, subject, roleKey string) identitysdk.AuthSession {
	role := binding.roles[roleKey]
	return identitysdk.AuthSession{
		SessionID: "plane-testkit-session", WorkspaceID: string(binding.application.WorkspaceID), AccessToken: token, RefreshToken: token, TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
		User: binding.users[subject], Roles: []identitysdk.Role{{ID: role.Key, Key: role.Key, Label: role.Name, Status: "active"}}, DefaultRole: role.Key, Permissions: append([]string(nil), role.Permissions...),
	}
}

func (binding *manifestIdentityBinding) accessBundle(subject, roleKey string) identitysdk.AccessBundle {
	role, found := binding.roles[strings.TrimSpace(roleKey)]
	if !found {
		role = IdentityFixtureRole{}
	}
	binding.mu.RLock()
	resources := map[identitysdk.ResourceType]bool{}
	for _, definitions := range binding.permissions {
		for _, definition := range definitions {
			if definition.SourceKind == "object_default" {
				resources[identitysdk.ResourceType(definition.ResourceKey)] = true
			}
		}
	}
	binding.mu.RUnlock()
	grants := []identitysdk.FunctionGrant{}
	grantKeys := map[string]bool{}
	addGrant := func(resource identitysdk.ResourceType, action identitysdk.Action) {
		key := string(resource) + "\x00" + string(action)
		if grantKeys[key] {
			return
		}
		grantKeys[key] = true
		grants = append(grants, identitysdk.FunctionGrant{Resource: resource, Action: action, Effect: identitysdk.EffectAllow})
	}
	for _, permission := range role.Permissions {
		permission = strings.TrimSpace(permission)
		separator := strings.LastIndex(permission, ".")
		if separator > 0 && separator < len(permission)-1 {
			addGrant(identitysdk.ResourceType(permission[:separator]), identitysdk.Action(permission[separator+1:]))
		}
	}
	dataPolicies := append([]identitysdk.DataPolicy(nil), role.DataPolicies...)
	for index, grant := range grants {
		hasExplicitPolicy := false
		for _, declared := range role.DataPolicies {
			if declared.Resource == grant.Resource && declared.Action == grant.Action {
				hasExplicitPolicy = true
				break
			}
		}
		if hasExplicitPolicy {
			continue
		}
		policy := identitysdk.DataPolicy{Key: fmt.Sprintf("plane-testkit-%s-%s-%d", grant.Resource, grant.Action, index), Resource: grant.Resource, Action: grant.Action, Effect: identitysdk.EffectAllow}
		switch {
		case !resources[grant.Resource]:
			// Runtime and module Actions have no business-record row scope. The
			// exact function grant is therefore expanded to an explicit all-scope
			// policy by the fixture compiler instead of being repeated by callers.
			policy.DataScopes = []identitysdk.DataScope{identitysdk.DataScopeAll}
		case role.AllowAllBusinessData:
			policy.DataScopes = []identitysdk.DataScope{identitysdk.DataScopeAll}
		case role.DataPredicate != nil:
			policy.Predicate = *role.DataPredicate
		default:
			continue
		}
		dataPolicies = append(dataPolicies, policy)
	}
	fieldPolicies := map[string]identitysdk.FieldPolicy{}
	if role.AllowAllBusinessData {
		for _, grant := range grants {
			if !resources[grant.Resource] {
				continue
			}
			key := string(grant.Resource) + "\x00*"
			policy := fieldPolicies[key]
			policy.Resource = grant.Resource
			policy.Field = "*"
			switch grant.Action {
			case "read":
				policy.Read = true
			case "export":
				policy.Export = true
			case "create", "update", "delete":
				policy.Write = true
			}
			fieldPolicies[key] = policy
		}
	}
	for _, policy := range role.FieldPolicies {
		fieldPolicies[string(policy.Resource)+"\x00"+strings.TrimSpace(policy.Field)] = policy
	}
	fields := make([]identitysdk.FieldPolicy, 0, len(fieldPolicies))
	for _, policy := range fieldPolicies {
		fields = append(fields, policy)
	}
	sort.Slice(fields, func(left, right int) bool {
		return string(fields[left].Resource)+"\x00"+fields[left].Field < string(fields[right].Resource)+"\x00"+fields[right].Field
	})
	return identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion, AuthorizationRevision: "plane-testkit-authorization", ExpiresAt: time.Now().Add(time.Hour),
		Subject: identitysdk.Subject{TenantID: binding.application.TenantID, WorkspaceID: binding.application.WorkspaceID, SubjectID: identitysdk.SubjectID(subject)}, FunctionGrants: grants, DataPolicies: dataPolicies, FieldPolicies: fields,
		ExportPolicies: append([]identitysdk.ExportPolicy(nil), role.ExportPolicies...), Guardrails: append([]identitysdk.Guardrail(nil), role.Guardrails...),
	}
}

var _ identitysdk.Factory = IdentityFactory{}
var _ identitysdk.Binding = (*manifestIdentityBinding)(nil)
var _ identitysdk.WorkflowWorkloadIdentityBinding = (*manifestIdentityBinding)(nil)
var _ identitysdk.WorkflowWorkloadIdentity = (*manifestIdentityBinding)(nil)
