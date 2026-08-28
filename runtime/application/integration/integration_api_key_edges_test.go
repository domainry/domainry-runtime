package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

type integrationAPIKeyEdgeRepository struct {
	*independentConfigRepository
	findValue integrationmodel.IntegrationAPIKey
	findOK    bool
	findErr   error
	listErr   error
	upsertErr error
	updateErr error
}

func newIntegrationAPIKeyEdgeRepository() *integrationAPIKeyEdgeRepository {
	return &integrationAPIKeyEdgeRepository{independentConfigRepository: &independentConfigRepository{apiKeys: map[string]integrationmodel.IntegrationAPIKey{}}}
}

func (r *integrationAPIKeyEdgeRepository) FindAPIKeyByTokenHash(context.Context, string, string) (integrationmodel.IntegrationAPIKey, bool, error) {
	return r.findValue, r.findOK, r.findErr
}

func (r *integrationAPIKeyEdgeRepository) ListAPIKeys(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationAPIKey, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.independentConfigRepository.ListAPIKeys(ctx, workspaceID)
}

func (r *integrationAPIKeyEdgeRepository) UpsertAPIKey(ctx context.Context, workspaceID string, value integrationmodel.IntegrationAPIKey) (integrationmodel.IntegrationAPIKey, error) {
	if r.upsertErr != nil {
		return integrationmodel.IntegrationAPIKey{}, r.upsertErr
	}
	return r.independentConfigRepository.UpsertAPIKey(ctx, workspaceID, value)
}

func (r *integrationAPIKeyEdgeRepository) UpdateAPIKeyLastUsed(ctx context.Context, workspaceID, key, usedAt string) (integrationmodel.IntegrationAPIKey, error) {
	if r.updateErr != nil {
		return integrationmodel.IntegrationAPIKey{}, r.updateErr
	}
	return r.independentConfigRepository.UpdateAPIKeyLastUsed(ctx, workspaceID, key, usedAt)
}

type integrationAPIKeyLimiter struct {
	decision ratelimit.Decision
	err      error
}

func (l integrationAPIKeyLimiter) Allow(context.Context, string, int, time.Duration) (ratelimit.Decision, error) {
	return l.decision, l.err
}

func integrationAPIKeyResolvedPrincipal() principalmodel.Principal {
	return integrationSDKPrincipal("service", "client", "customer.read", "customer.write")
}

func TestPrincipalFromIntegrationAPIKeyRejectsInvalidTokenStates(t *testing.T) {
	repository := newIntegrationAPIKeyEdgeRepository()
	resolved := integrationAPIKeyResolvedPrincipal()
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		PrincipalResolver: func(context.Context, string, string, string) principalmodel.Principal {
			return resolved
		},
	})
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "", "request"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), " ", "workspace", "request"); apperror.CodeOf(err) != "auth.token_required" {
		t.Fatalf("token guard=%v", err)
	}
	repository.findErr = errors.New("lookup failed")
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request"); !errors.Is(err, repository.findErr) {
		t.Fatalf("lookup error=%v", err)
	}
	repository.findErr = nil
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request"); apperror.CodeOf(err) != "auth.invalid_token" {
		t.Fatalf("missing key=%v", err)
	}
	repository.findOK = true
	for name, key := range map[string]integrationmodel.IntegrationAPIKey{
		"disabled":           {Status: "disabled", WorkspaceID: "workspace"},
		"workspace mismatch": {Status: "active", WorkspaceID: "other"},
		"invalid expiry":     {Status: "active", WorkspaceID: "workspace", ExpiresAt: "invalid"},
		"expired":            {Status: "active", WorkspaceID: "workspace", ExpiresAt: "2020-01-01T00:00:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			repository.findValue = key
			_, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request")
			want := "auth.invalid_token"
			if name == "invalid expiry" || name == "expired" {
				want = "auth.session_expired"
			}
			if apperror.CodeOf(err) != want {
				t.Fatalf("error=%v want=%q", err, want)
			}
		})
	}
	repository.findValue = integrationmodel.IntegrationAPIKey{Key: "key", Status: "active", WorkspaceID: "workspace", ActorID: "actor", RoleKey: "role"}
	service.resolvePrincipal = func(context.Context, string, string, string) principalmodel.Principal {
		return principalmodel.Principal{}
	}
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request"); apperror.CodeOf(err) != "auth.invalid_token" {
		t.Fatalf("unknown principal=%v", err)
	}
}

func TestPrincipalFromIntegrationAPIKeyRateLimitAndLastUsedEdges(t *testing.T) {
	key := integrationmodel.IntegrationAPIKey{Key: "key", Status: "active", WorkspaceID: "workspace", ActorID: "actor", RoleKey: "role", Scopes: []string{"customer.read", "rate_limit:1/minute"}}
	newService := func(repository *integrationAPIKeyEdgeRepository, limiter integrationAPIKeyLimiter, audits *[]string) *IntegrationApplicationService {
		return NewIntegrationApplicationService(ApplicationDependencies{
			ConfigRepository: repository,
			APILimiter:       limiter,
			PrincipalResolver: func(context.Context, string, string, string) principalmodel.Principal {
				return integrationAPIKeyResolvedPrincipal()
			},
			Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
				*audits = append(*audits, event)
			},
		})
	}
	repository := newIntegrationAPIKeyEdgeRepository()
	repository.findValue, repository.findOK = key, true
	audits := []string{}
	limitErr := errors.New("limiter failed")
	service := newService(repository, integrationAPIKeyLimiter{err: limitErr}, &audits)
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request"); !errors.Is(err, limitErr) || audits[len(audits)-1] != "integration_api_key_rate_limited" {
		t.Fatalf("limiter error=%v audits=%#v", err, audits)
	}
	service = newService(repository, integrationAPIKeyLimiter{decision: ratelimit.Decision{Allowed: false}}, &audits)
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request"); apperror.CodeOf(err) != "backend.integration.api_key.rate_limited" {
		t.Fatalf("rate limit=%v", err)
	}
	repository.apiKeys["workspace:key"] = key
	repository.updateErr = errors.New("last-used write failed")
	service = newService(repository, integrationAPIKeyLimiter{decision: ratelimit.Decision{Allowed: true}}, &audits)
	principal, used, err := service.PrincipalFromIntegrationAPIKey(t.Context(), "token", "workspace", "request")
	if err != nil || principal.RequestID != "request" || used.Key != "key" || !principal.HasPermission("customer.read") || principal.HasPermission("customer.write") || audits[len(audits)-1] != "integration_api_key_used" {
		t.Fatalf("principal=%#v key=%#v audits=%#v err=%v", principal, used, audits, err)
	}
}

func TestIntegrationAPIKeyMutationFailureEdges(t *testing.T) {
	repository := newIntegrationAPIKeyEdgeRepository()
	admin := integrationSDKPrincipal("admin", "admin", "workspace.admin")
	admin.WorkspaceID = "workspace"
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		PrincipalResolver: func(context.Context, string, string, string) principalmodel.Principal {
			return integrationAPIKeyResolvedPrincipal()
		},
	})
	if _, err := service.CreateIntegrationAPIKey(t.Context(), integrationmodel.IntegrationAPIKeyCreateRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("create authorization error=%v", err)
	}
	repository.upsertErr = errors.New("write failed")
	if _, err := service.CreateIntegrationAPIKey(t.Context(), integrationmodel.IntegrationAPIKeyCreateRequest{Name: "Generated", ActorID: "actor", RoleKey: "role"}, admin); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("create write error=%v", err)
	}
	repository.upsertErr = nil
	created, err := service.CreateIntegrationAPIKey(t.Context(), integrationmodel.IntegrationAPIKeyCreateRequest{Name: "Generated Key", ActorID: "actor", RoleKey: "role"}, admin)
	if err != nil || created.APIKey.Key == "" || !strings.HasPrefix(created.Token, APIKeyTokenPrefix) {
		t.Fatalf("created=%#v err=%v", created, err)
	}

	for _, mutate := range []struct {
		name string
		call func(principalmodel.Principal) error
	}{
		{"disable", func(principal principalmodel.Principal) error {
			_, err := service.DisableIntegrationAPIKey(t.Context(), "key", principal)
			return err
		}},
		{"rotate", func(principal principalmodel.Principal) error {
			_, err := service.RotateIntegrationAPIKey(t.Context(), "key", principal)
			return err
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			if err := mutate.call(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
				t.Fatalf("workspace guard=%v", err)
			}
			if err := mutate.call(integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
				t.Fatalf("permission guard=%v", err)
			}
			repository.listErr = errors.New("list failed")
			if err := mutate.call(admin); !errors.Is(err, repository.listErr) {
				t.Fatalf("list error=%v", err)
			}
			repository.listErr = nil
			if err := mutate.call(admin); apperror.KindOf(err) != apperror.KindNotFound {
				t.Fatalf("missing key=%v", err)
			}
			repository.apiKeys["workspace:key"] = integrationmodel.IntegrationAPIKey{Key: "key", WorkspaceID: "workspace", Status: "active"}
			repository.upsertErr = errors.New("write failed")
			if err := mutate.call(admin); !errors.Is(err, repository.upsertErr) {
				t.Fatalf("write error=%v", err)
			}
			repository.upsertErr = nil
			delete(repository.apiKeys, "workspace:key")
		})
	}
}

func TestAPIKeyRateLimitConfigParsingEdges(t *testing.T) {
	for _, raw := range []string{"", "off", "disabled", "0", "invalid", "0/m", "1/invalid", "1/-1s"} {
		if config := ParseAPIKeyRateLimit(raw, "test"); config.Enabled {
			t.Fatalf("invalid config %q enabled: %#v", raw, config)
		}
	}
	t.Setenv("INTEGRATION_API_KEY_RATE_LIMIT", "")
	if err := NewIntegrationApplicationService(ApplicationDependencies{}).checkAPIKeyRateLimit(t.Context(), integrationmodel.IntegrationAPIKey{}); err != nil {
		t.Fatalf("disabled rate limit error=%v", err)
	}
	for raw, window := range map[string]time.Duration{"1/s": time.Second, "2/minute": time.Minute, "3/hour": time.Hour, "4/250ms": 250 * time.Millisecond} {
		config := ParseAPIKeyRateLimit(raw, "test")
		if !config.Enabled || config.Window != window || config.Source != "test:"+raw {
			t.Fatalf("config %q=%#v", raw, config)
		}
	}
	if APIKeyRateLimitFromScope("customer.read").Enabled || !APIKeyRateLimitFromScope("rate_limit:2/minute").Enabled {
		t.Fatal("scope rate limit parsing mismatch")
	}
	key := integrationmodel.IntegrationAPIKey{Scopes: []string{"customer.read", "rate_limit:3/hour"}}
	if config := APIKeyRateLimitConfig(key); !config.Enabled || config.Limit != 3 {
		t.Fatalf("key config=%#v", config)
	}
	t.Setenv("INTEGRATION_API_KEY_RATE_LIMIT", "7/seconds")
	if config := APIKeyRateLimitConfig(integrationmodel.IntegrationAPIKey{Scopes: []string{"customer.read"}}); !config.Enabled || config.Limit != 7 || config.Source != "env:7/seconds" {
		t.Fatalf("environment config=%#v", config)
	}
}

func TestIntegrationAPIKeyScopeProjectionEdges(t *testing.T) {
	principal := integrationSDKPrincipal("service", "client", "customer.read", "customer.write", "invoice.read")
	if !apiKeyScopesAllowed(principal, []string{"customer.read", "rate_limit:1/s", "integration.entrypoint.invoke"}) {
		t.Fatal("entrypoint scope was rejected")
	}
	if apiKeyScopesAllowed(principal, []string{"unknown.read"}) {
		t.Fatal("unknown SDK permission scope was accepted")
	}
	if shape := apiKeyAuditShape(integrationmodel.IntegrationAPIKey{}); shape["scopes"] == nil {
		t.Fatal("nil scopes were not normalized")
	}
}
