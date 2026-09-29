package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/ratelimit"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type integrationAPIKeyPrincipalProvider struct {
	authentication     integrationsdk.APIKeyAuthentication
	principals         identitysdk.PrincipalResolver
	rateLimiter        ratelimit.Limiter
	rateLimitPerMinute int
}

func newIntegrationAPIKeyPrincipalProvider(binding integrationsdk.Binding, principals identitysdk.PrincipalResolver, limiter ratelimit.Limiter, rateLimitPerMinute int) runtimehttp.IntegrationAuthenticationPrincipalProvider {
	if binding == nil {
		return nil
	}
	authenticationBinding, ok := binding.(integrationsdk.APIKeyAuthenticationBinding)
	if !ok || authenticationBinding.APIKeyAuthentication() == nil {
		panic("transport.AssembleRuntimeHTTPServer requires Integration API key authentication")
	}
	if principals == nil {
		panic("transport.AssembleRuntimeHTTPServer requires Identity principal resolution for Integration API keys")
	}
	return &integrationAPIKeyPrincipalProvider{
		authentication:     authenticationBinding.APIKeyAuthentication(),
		principals:         principals,
		rateLimiter:        limiter,
		rateLimitPerMinute: rateLimitPerMinute,
	}
}

func (p *integrationAPIKeyPrincipalProvider) PrincipalFromIntegrationAPIKey(ctx context.Context, token, workspaceID, requestID string) (principalmodel.Principal, integrationsdk.APIKey, error) {
	if p == nil || p.authentication == nil || p.principals == nil {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindUnavailable, "backend.integration.api_key_authentication_unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if strings.TrimSpace(token) == "" || workspaceID == "" {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
	}
	key, err := p.authentication.AuthenticateAPIKey(ctx, token, workspaceID)
	if err != nil {
		if errors.Is(err, integrationsdk.ErrAPIKeyInvalid) {
			return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
		}
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindUnavailable, "backend.integration.api_key_authentication_unavailable")
	}
	if !validAuthenticatedAPIKey(key, workspaceID) {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
	}
	if p.rateLimiter != nil && p.rateLimitPerMinute > 0 {
		decision, rateErr := p.rateLimiter.Allow(ctx, "runtime_api_key:"+workspaceID+":"+key.Key, p.rateLimitPerMinute, time.Minute)
		if rateErr != nil {
			return principalmodel.Principal{}, integrationsdk.APIKey{}, apperror.New(apperror.KindUnavailable, "backend.integration.api_key.rate_limit_unavailable", rateErr, nil)
		}
		if !decision.Allowed {
			return principalmodel.Principal{}, key, integrationAPIKeyError(apperror.KindRateLimited, "backend.integration.api_key.rate_limited")
		}
	}

	resolution, err := p.principals.Resolve(requestcontext.WithWorkspaceID(ctx, workspaceID), identitysdk.PrincipalResolutionRequest{
		SubjectID: identitysdk.SubjectID(key.ActorID),
		RoleKey:   key.RoleKey,
	})
	if err != nil {
		var identityErr *identitysdk.Error
		if errors.As(err, &identityErr) && identityErr.StatusCode >= http.StatusInternalServerError {
			return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindUnavailable, "backend.integration.api_key_authentication_unavailable")
		}
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
	}
	if !validAPIKeyPrincipalResolution(resolution, key) {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
	}

	bundle, ok := scopeAPIKeyAccessBundle(resolution.AccessBundle, key)
	if !ok {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, integrationAPIKeyError(apperror.KindForbidden, "auth.invalid_api_key")
	}
	// Identity validates the source bundle before returning it. Validate the
	// narrowed derivative again at this trust boundary so record authorization
	// cannot later collapse a malformed API-key bundle into a generic 403.
	if err := bundle.Validate(time.Now().UTC()); err != nil {
		return principalmodel.Principal{}, integrationsdk.APIKey{}, apperror.New(apperror.KindUnavailable, "backend.integration.api_key_authorization_unavailable", err, nil)
	}
	resolution.Principal.AccessBundle = &bundle
	resolution.Principal.AuthorizationRevision = string(bundle.AuthorizationRevision)
	resolution.Principal.Permissions = resolution.Principal.PermissionKeys()
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, strings.TrimSpace(requestID)), key, nil
}

func integrationAPIKeyError(kind apperror.ErrorKind, code string) error {
	return apperror.New(kind, code, errors.New(code), nil)
}

func validAuthenticatedAPIKey(key integrationsdk.APIKey, workspaceID string) bool {
	if strings.TrimSpace(key.Key) == "" || strings.TrimSpace(key.WorkspaceID) != workspaceID || strings.TrimSpace(key.ActorID) == "" || strings.TrimSpace(key.RoleKey) == "" || strings.TrimSpace(key.Status) != "active" {
		return false
	}
	_, ok := exactAPIKeyScopes(key.Scopes)
	return ok
}

func validAPIKeyPrincipalResolution(resolution identitysdk.PrincipalResolution, key integrationsdk.APIKey) bool {
	principal := resolution.Principal
	return principal.Known &&
		strings.TrimSpace(principal.User.AccountType) == "service" &&
		strings.TrimSpace(principal.WorkspaceID) == strings.TrimSpace(key.WorkspaceID) &&
		strings.TrimSpace(principal.UserID) == strings.TrimSpace(key.ActorID) &&
		strings.TrimSpace(principal.RoleKey) == strings.TrimSpace(key.RoleKey) &&
		strings.TrimSpace(principal.AuthorizationRevision) != "" &&
		strings.TrimSpace(string(resolution.AccessBundle.AuthorizationRevision)) != "" &&
		strings.TrimSpace(string(resolution.AccessBundle.Subject.WorkspaceID)) == strings.TrimSpace(key.WorkspaceID) &&
		strings.TrimSpace(string(resolution.AccessBundle.Subject.SubjectID)) == strings.TrimSpace(key.ActorID)
}

func scopeAPIKeyAccessBundle(source identitysdk.AccessBundle, key integrationsdk.APIKey) (identitysdk.AccessBundle, bool) {
	scopes, ok := exactAPIKeyScopes(key.Scopes)
	if !ok {
		return identitysdk.AccessBundle{}, false
	}
	bundle := source
	bundle.FunctionGrants = nil
	for _, grant := range source.FunctionGrants {
		if scopes[permissionKey(grant.Resource, grant.Action)] {
			bundle.FunctionGrants = append(bundle.FunctionGrants, grant)
		}
	}
	bundle.DataPolicies = nil
	for _, policy := range source.DataPolicies {
		if scopes[permissionKey(policy.Resource, policy.Action)] {
			bundle.DataPolicies = append(bundle.DataPolicies, policy)
		}
	}
	bundle.FieldPolicies = nil
	for _, policy := range source.FieldPolicies {
		if apiKeyScopeIncludesResource(scopes, string(policy.Resource)) {
			bundle.FieldPolicies = append(bundle.FieldPolicies, policy)
		}
	}
	bundle.ReferencePolicies = nil
	for _, policy := range source.ReferencePolicies {
		if apiKeyScopeIncludesResource(scopes, string(policy.SourceResource)) {
			bundle.ReferencePolicies = append(bundle.ReferencePolicies, policy)
		}
	}
	bundle.ExportPolicies = nil
	for _, policy := range source.ExportPolicies {
		if scopes[strings.TrimSpace(string(policy.Resource))+".export"] {
			bundle.ExportPolicies = append(bundle.ExportPolicies, policy)
		}
	}
	// Guardrails are deny-only global safety policy. Keep all of them so an API
	// key can narrow current authority but can never remove an Identity denial.
	bundle.Guardrails = append([]identitysdk.Guardrail(nil), source.Guardrails...)
	bundle.AuthorizationRevision = identitysdk.AuthorizationRevision(apiKeyAuthorizationRevision(source.AuthorizationRevision, key))
	return bundle, true
}

func exactAPIKeyScopes(values []string) (map[string]bool, bool) {
	if len(values) == 0 || len(values) > 256 {
		return nil, false
	}
	scopes := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		separator := strings.LastIndexByte(value, '.')
		if value == "" || separator <= 0 || separator == len(value)-1 || strings.ContainsAny(value, "* \t\r\n") {
			return nil, false
		}
		scopes[value] = true
	}
	return scopes, len(scopes) > 0
}

func permissionKey(resource identitysdk.ResourceType, action identitysdk.Action) string {
	return strings.TrimSpace(string(resource)) + "." + strings.TrimSpace(string(action))
}

func apiKeyScopeIncludesResource(scopes map[string]bool, resource string) bool {
	prefix := strings.TrimSpace(resource) + "."
	if prefix == "." {
		return false
	}
	for scope := range scopes {
		if strings.HasPrefix(scope, prefix) {
			return true
		}
	}
	return false
}

func apiKeyAuthorizationRevision(identityRevision identitysdk.AuthorizationRevision, key integrationsdk.APIKey) string {
	scopes := append([]string(nil), key.Scopes...)
	sort.Strings(scopes)
	payload, _ := json.Marshal(struct {
		Contract         string   `json:"contract"`
		IdentityRevision string   `json:"identity_revision"`
		WorkspaceID      string   `json:"workspace_id"`
		Key              string   `json:"key"`
		UpdatedAt        string   `json:"updated_at"`
		Scopes           []string `json:"scopes"`
	}{
		Contract:         "runtime.api_key_scope.v1",
		IdentityRevision: strings.TrimSpace(string(identityRevision)),
		WorkspaceID:      strings.TrimSpace(key.WorkspaceID),
		Key:              strings.TrimSpace(key.Key),
		UpdatedAt:        strings.TrimSpace(key.UpdatedAt),
		Scopes:           scopes,
	})
	digest := sha256.Sum256(payload)
	return "api-key:" + hex.EncodeToString(digest[:])
}

var _ runtimehttp.IntegrationAuthenticationPrincipalProvider = (*integrationAPIKeyPrincipalProvider)(nil)
