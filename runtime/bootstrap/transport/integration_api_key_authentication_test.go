package transport

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/ratelimit"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

type apiKeyAuthenticationStub struct {
	key integrationsdk.APIKey
	err error
}

func (s apiKeyAuthenticationStub) AuthenticateAPIKey(context.Context, string, string) (integrationsdk.APIKey, error) {
	return s.key, s.err
}

type apiKeyPrincipalResolverStub struct {
	resolution identitysdk.PrincipalResolution
	err        error
	requests   []identitysdk.PrincipalResolutionRequest
}

func (s *apiKeyPrincipalResolverStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	s.requests = append(s.requests, request)
	return s.resolution, s.err
}

func TestIntegrationAPIKeyPrincipalProviderIntersectsCurrentIdentityAuthority(t *testing.T) {
	key := integrationsdk.APIKey{
		Key: "sales-reader", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "sales",
		Scopes: []string{"account.read"}, Status: "active", UpdatedAt: "2026-09-29T08:00:00Z",
	}
	resolver := &apiKeyPrincipalResolverStub{resolution: apiKeyPrincipalResolution()}
	if err := resolver.resolution.AccessBundle.Validate(time.Now().UTC()); err != nil {
		t.Fatalf("source access bundle fixture is invalid: %v", err)
	}
	provider := &integrationAPIKeyPrincipalProvider{authentication: apiKeyAuthenticationStub{key: key}, principals: resolver}

	principal, authenticatedKey, err := provider.PrincipalFromIntegrationAPIKey(t.Context(), "itg_private", "workspace-a", "request-a")
	if err != nil {
		t.Fatal(err)
	}
	if authenticatedKey.Key != key.Key || principal.RequestID != "request-a" || !principal.Known || principal.UserID != "service-a" || principal.RoleKey != "sales" {
		t.Fatalf("unexpected resolved principal: %#v key=%#v", principal, authenticatedKey)
	}
	if len(resolver.requests) != 1 || resolver.requests[0].SubjectID != "service-a" || resolver.requests[0].RoleKey != "sales" {
		t.Fatalf("unexpected Identity resolution request: %#v", resolver.requests)
	}
	if !principal.HasPermission("account.read") {
		t.Fatal("expected scoped account.read permission")
	}
	if principal.HasPermission("account.update") || principal.HasPermission("opportunity.read") {
		t.Fatalf("API key scopes leaked current Identity authority: %#v", principal.PermissionKeys())
	}
	if principal.AuthorizationRevision == "identity-revision" || principal.AuthorizationRevision == "" || principal.AccessBundle == nil || string(principal.AccessBundle.AuthorizationRevision) != principal.AuthorizationRevision {
		t.Fatalf("API key scope was not bound into authorization revision: %#v", principal)
	}
	if len(principal.AccessBundle.FieldPolicies) != 1 || string(principal.AccessBundle.FieldPolicies[0].Resource) != "account" {
		t.Fatalf("unexpected scoped field policies: %#v", principal.AccessBundle.FieldPolicies)
	}
	if len(principal.AccessBundle.ReferencePolicies) != 1 || string(principal.AccessBundle.ReferencePolicies[0].SourceResource) != "account" {
		t.Fatalf("unexpected scoped reference policies: %#v", principal.AccessBundle.ReferencePolicies)
	}
	if len(principal.AccessBundle.ExportPolicies) != 0 {
		t.Fatalf("export policy survived without account.export scope: %#v", principal.AccessBundle.ExportPolicies)
	}
	if len(principal.AccessBundle.Guardrails) != 1 {
		t.Fatalf("Identity guardrails were not preserved: %#v", principal.AccessBundle.Guardrails)
	}
	if err := principal.AccessBundle.Validate(time.Now().UTC()); err != nil {
		t.Fatalf("scoped API key access bundle is invalid: %v", err)
	}
}

func TestIntegrationAPIKeyPrincipalProviderFailsClosedForStaleIdentityAndInvalidScopes(t *testing.T) {
	tests := []struct {
		name       string
		key        integrationsdk.APIKey
		resolution identitysdk.PrincipalResolution
	}{
		{name: "wrong workspace", key: integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-b", ActorID: "service-a", RoleKey: "sales", Scopes: []string{"account.read"}, Status: "active"}, resolution: apiKeyPrincipalResolution()},
		{name: "wildcard scope", key: integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "sales", Scopes: []string{"account.*"}, Status: "active"}, resolution: apiKeyPrincipalResolution()},
		{name: "human actor", key: integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "sales", Scopes: []string{"account.read"}, Status: "active"}, resolution: func() identitysdk.PrincipalResolution {
			value := apiKeyPrincipalResolution()
			value.Principal.User.AccountType = "human"
			return value
		}()},
		{name: "revoked actor", key: integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "sales", Scopes: []string{"account.read"}, Status: "active"}, resolution: func() identitysdk.PrincipalResolution {
			value := apiKeyPrincipalResolution()
			value.Principal.Known = false
			return value
		}()},
		{name: "role changed", key: integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "administrator", Scopes: []string{"account.read"}, Status: "active"}, resolution: apiKeyPrincipalResolution()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &integrationAPIKeyPrincipalProvider{
				authentication: apiKeyAuthenticationStub{key: test.key},
				principals:     &apiKeyPrincipalResolverStub{resolution: test.resolution},
			}
			if _, _, err := provider.PrincipalFromIntegrationAPIKey(t.Context(), "itg_private", "workspace-a", "request-a"); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("expected forbidden failure, got %v", err)
			}
		})
	}
}

func TestIntegrationAPIKeyPrincipalProviderRateLimitsPerKey(t *testing.T) {
	key := integrationsdk.APIKey{Key: "key", WorkspaceID: "workspace-a", ActorID: "service-a", RoleKey: "sales", Scopes: []string{"account.read"}, Status: "active"}
	provider := &integrationAPIKeyPrincipalProvider{
		authentication:     apiKeyAuthenticationStub{key: key},
		principals:         &apiKeyPrincipalResolverStub{resolution: apiKeyPrincipalResolution()},
		rateLimiter:        ratelimit.NewMemoryLimiter(10),
		rateLimitPerMinute: 1,
	}
	if _, _, err := provider.PrincipalFromIntegrationAPIKey(t.Context(), "itg_private", "workspace-a", "request-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.PrincipalFromIntegrationAPIKey(t.Context(), "itg_private", "workspace-a", "request-b"); err == nil || apperror.CodeOf(err) != "backend.integration.api_key.rate_limited" || apperror.KindOf(err) != apperror.KindRateLimited {
		t.Fatalf("expected stable per-key rate limit, got %v", err)
	}
}

func TestIntegrationAPIKeyPrincipalProviderPreservesAuthenticationAvailability(t *testing.T) {
	provider := &integrationAPIKeyPrincipalProvider{
		authentication: apiKeyAuthenticationStub{err: integrationsdk.ErrAPIKeyAuthenticationUnavailable},
		principals:     &apiKeyPrincipalResolverStub{resolution: apiKeyPrincipalResolution()},
	}
	if _, _, err := provider.PrincipalFromIntegrationAPIKey(t.Context(), "itg_private", "workspace-a", "request-a"); err == nil || apperror.KindOf(err) != apperror.KindUnavailable || apperror.CodeOf(err) != "backend.integration.api_key_authentication_unavailable" {
		t.Fatalf("expected availability error, got %v", err)
	}
}

func apiKeyPrincipalResolution() identitysdk.PrincipalResolution {
	bundle := identitysdk.AccessBundle{
		ContractVersion:       identitysdk.CurrentPolicyBundleVersion,
		AuthorizationRevision: "identity-revision",
		ExpiresAt:             time.Now().UTC().Add(time.Hour),
		Subject: identitysdk.Subject{
			WorkspaceID: "workspace-a",
			SubjectID:   "service-a",
		},
		FunctionGrants: []identitysdk.FunctionGrant{
			{Resource: "account", Action: "read", Effect: identitysdk.EffectAllow},
			{Resource: "account", Action: "update", Effect: identitysdk.EffectAllow},
			{Resource: "opportunity", Action: "read", Effect: identitysdk.EffectAllow},
		},
		DataPolicies: []identitysdk.DataPolicy{
			{Key: "account-read", Resource: "account", Action: "read", Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}},
			{Key: "account-update", Resource: "account", Action: "update", Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}},
			{Key: "opportunity-read", Resource: "opportunity", Action: "read", Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}},
		},
		FieldPolicies: []identitysdk.FieldPolicy{
			{Resource: "account", Field: "name", Read: true},
			{Resource: "opportunity", Field: "name", Read: true},
		},
		ReferencePolicies: []identitysdk.ReferencePolicy{
			{SourceResource: "account", Reference: "owner", TargetResource: "user", DisplayFields: []string{"name"}, Allowed: true},
			{SourceResource: "opportunity", Reference: "account", TargetResource: "account", DisplayFields: []string{"name"}, Allowed: true},
		},
		ExportPolicies: []identitysdk.ExportPolicy{
			{Resource: "account", Mode: identitysdk.ExportModeAllowList, Fields: []string{"name"}},
		},
		Guardrails: []identitysdk.Guardrail{
			{Key: "blocked-account-read", Resource: "account", Action: "read", Effect: identitysdk.EffectDeny, Predicate: &identitysdk.Predicate{Fact: "blocked", Operator: identitysdk.OperatorEqual, Value: true}},
		},
	}
	return identitysdk.PrincipalResolution{
		Principal: identitysdk.Principal{
			ContractVersion:       identitysdk.PrincipalContextContractVersion,
			Known:                 true,
			WorkspaceID:           "workspace-a",
			UserID:                "service-a",
			RoleKey:               "sales",
			AuthorizationRevision: "identity-revision",
			User:                  identitysdk.User{ID: "service-a", AccountType: "service", Status: "active"},
		},
		AccessBundle: bundle,
	}
}
