package testkit

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
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
		Roles: []IdentityFixtureRole{{Key: "admin", Name: "Administrator", Permissions: []string{"workspace.admin"}, AllowAllBusinessData: true}},
		Users: []identitysdk.User{
			{ID: "admin", Name: "Administrator", Status: "active"},
			{ID: "runtime_fixture_user", Name: "Runtime fixture user", Email: "runtime-fixture@example.com", Status: "active"},
		},
		UserRoleAssignments: map[string][]string{"admin": {"admin"}, "runtime_fixture_user": {"admin"}},
	})
}

func (factory IdentityFactory) Open(_ context.Context, application identitysdk.ApplicationRef) (identitysdk.Binding, error) {
	if !application.WorkspaceID.Valid() || !application.ApplicationKey.Valid() {
		return nil, &identitysdk.Error{Code: "identity.application_scope_invalid"}
	}
	return &manifestIdentityBinding{
		application: application, roles: factory.roles, users: factory.users, userRoles: factory.userRoles,
		sessions: map[string]manifestIdentitySession{},
	}, nil
}

type manifestIdentitySession struct {
	subject string
	role    string
}

type manifestIdentityBinding struct {
	mu          sync.RWMutex
	application identitysdk.ApplicationRef
	roles       map[string]IdentityFixtureRole
	users       map[string]identitysdk.User
	userRoles   map[string][]string
	sessions    map[string]manifestIdentitySession
	catalog     identitysdk.AuthorizationCatalog
}

func (binding *manifestIdentityBinding) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{ProtocolVersion: identitysdk.CurrentProtocolVersion, BundleVersion: identitysdk.CurrentPolicyBundleVersion, CatalogVersion: identitysdk.CatalogVersionV1, Mode: identitysdk.DeploymentModeModule, Issuer: "plane-testkit-identity", Audience: string(binding.application.ApplicationKey)}
}
func (binding *manifestIdentityBinding) Authentication() identitysdk.Authentication { return binding }
func (binding *manifestIdentityBinding) Tokens() identitysdk.TokenVerifier          { return binding }
func (binding *manifestIdentityBinding) Authorization() identitysdk.Authorization   { return binding }
func (binding *manifestIdentityBinding) Principals() identitysdk.PrincipalResolver  { return binding }
func (binding *manifestIdentityBinding) Directory() identitysdk.Directory           { return binding }
func (binding *manifestIdentityBinding) Catalog() identitysdk.CatalogClient         { return binding }
func (binding *manifestIdentityBinding) Credentials() identitysdk.CredentialManager { return binding }
func (binding *manifestIdentityBinding) Close(context.Context) error                { return nil }

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
		Issuer: "plane-testkit-identity", Audience: binding.application.ApplicationKey, SubjectID: identitysdk.SubjectID(session.subject), WorkspaceID: binding.application.WorkspaceID,
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

func (binding *manifestIdentityBinding) FindUser(_ context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	user, found := binding.users[string(lookup.UserID)]
	return user, found, nil
}
func (binding *manifestIdentityBinding) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}
func (binding *manifestIdentityBinding) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
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
func (binding *manifestIdentityBinding) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
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
func (binding *manifestIdentityBinding) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

func (binding *manifestIdentityBinding) Validate(_ context.Context, catalog identitysdk.AuthorizationCatalog) error {
	return catalog.ValidateContract()
}
func (binding *manifestIdentityBinding) Publish(_ context.Context, catalog identitysdk.AuthorizationCatalog) (identitysdk.CatalogReceipt, error) {
	if err := catalog.ValidateContract(); err != nil {
		return identitysdk.CatalogReceipt{}, err
	}
	binding.mu.Lock()
	binding.catalog = catalog
	binding.mu.Unlock()
	return identitysdk.CatalogReceipt{Revision: "plane-testkit-catalog", PublishedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}
func (binding *manifestIdentityBinding) CurrentRevision(context.Context, identitysdk.ApplicationRef) (identitysdk.CatalogReceipt, error) {
	return identitysdk.CatalogReceipt{Revision: "plane-testkit-catalog"}, nil
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
	catalog := binding.catalog
	binding.mu.RUnlock()
	resources := map[identitysdk.ResourceType]identitysdk.ResourceDefinition{}
	for _, resource := range catalog.Resources {
		resources[resource.Key] = resource
	}
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
	admin := false
	for _, permission := range role.Permissions {
		permission = strings.TrimSpace(permission)
		admin = admin || permission == "workspace.admin"
		separator := strings.LastIndex(permission, ".")
		if separator > 0 && separator < len(permission)-1 {
			addGrant(identitysdk.ResourceType(permission[:separator]), identitysdk.Action(permission[separator+1:]))
		}
	}
	if admin {
		for _, action := range catalog.Actions {
			addGrant(action.Resource, action.Action)
		}
	}
	dataPolicies := append([]identitysdk.DataPolicy(nil), role.DataPolicies...)
	for index, grant := range grants {
		if _, businessResource := resources[grant.Resource]; !businessResource {
			continue
		}
		predicate := identitysdk.Predicate{}
		switch {
		case admin || role.AllowAllBusinessData:
			predicate = identitysdk.Predicate{Fact: "id", Operator: identitysdk.OperatorExists, Value: true}
		case role.DataPredicate != nil:
			predicate = *role.DataPredicate
		default:
			continue
		}
		dataPolicies = append(dataPolicies, identitysdk.DataPolicy{Key: fmt.Sprintf("plane-testkit-%s-%s-%d", grant.Resource, grant.Action, index), Resource: grant.Resource, Action: grant.Action, Effect: identitysdk.EffectAllow, Predicate: predicate})
	}
	fieldPolicies := map[string]identitysdk.FieldPolicy{}
	actionsByResource := map[identitysdk.ResourceType]map[identitysdk.Action]bool{}
	for _, grant := range grants {
		if actionsByResource[grant.Resource] == nil {
			actionsByResource[grant.Resource] = map[identitysdk.Action]bool{}
		}
		actionsByResource[grant.Resource][grant.Action] = grant.Effect == identitysdk.EffectAllow
	}
	for resourceKey, resource := range resources {
		actions := actionsByResource[resourceKey]
		read := admin || actions["read"] || actions["list"] || actions["search"] || actions["report"] || actions["audit"]
		write := admin || actions["create"] || actions["update"] || actions["write"]
		export := admin || actions["export"]
		for _, field := range resource.Fields {
			fieldPolicies[string(resourceKey)+"\x00"+field] = identitysdk.FieldPolicy{Resource: resourceKey, Field: field, Read: read, Write: write, Export: export}
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
		ContractVersion: identitysdk.CurrentPolicyBundleVersion, CatalogRevision: "plane-testkit-catalog", AuthorizationRevision: "plane-testkit-authorization", ExpiresAt: time.Now().Add(time.Hour),
		Subject: identitysdk.Subject{WorkspaceID: binding.application.WorkspaceID, SubjectID: identitysdk.SubjectID(subject)}, FunctionGrants: grants, DataPolicies: dataPolicies, FieldPolicies: fields,
		ExportPolicies: append([]identitysdk.ExportPolicy(nil), role.ExportPolicies...), Guardrails: append([]identitysdk.Guardrail(nil), role.Guardrails...),
	}
}

var _ identitysdk.Factory = IdentityFactory{}
var _ identitysdk.Binding = (*manifestIdentityBinding)(nil)
