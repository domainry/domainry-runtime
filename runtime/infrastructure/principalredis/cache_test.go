package principalredis

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	identity "github.com/domainry/domainry-identity-sdk/authorization"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	redis "github.com/redis/go-redis/v9"
)

func TestCacheRoundTripPreservesAccessBundleAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := New(client, "test:principal:")
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1", AuthorizationRevision: "revision-1", TokenID: "token-1"}
	entry := cacheTestEntry(now.Add(5 * time.Minute))
	if err := cache.Set(t.Context(), key, entry, now); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := cache.Get(t.Context(), key, now)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !resolved.Principal.HasPermission("orders.read") || !resolved.ExpiresAt.Equal(entry.ExpiresAt) {
		t.Fatalf("resolved=%#v found=%v", resolved, found)
	}
	for _, redisKey := range server.Keys() {
		if strings.Contains(redisKey, "workspace-1") || strings.Contains(redisKey, "user-1") || strings.Contains(redisKey, "token-1") {
			t.Fatalf("Redis cache exposed raw identity material in key %q", redisKey)
		}
		if strings.Contains(redisKey, "entry:") {
			value, err := server.Get(redisKey)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(value, "access-token") {
				t.Fatalf("Redis cache exposed an access token: %q", value)
			}
		}
	}
	server.FastForward(4 * time.Minute)
	if _, found, err := cache.Get(t.Context(), key, now.Add(4*time.Minute)); err != nil || !found {
		t.Fatalf("entry before absolute expiry found=%v err=%v", found, err)
	}
	server.FastForward(time.Minute)
	if _, found, err := cache.Get(t.Context(), key, now.Add(5*time.Minute)); err != nil || found {
		t.Fatalf("cache read extended absolute expiry: found=%v err=%v", found, err)
	}
}

func TestCacheInvalidatesOnlyMatchingSubject(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := New(client, "test:principal:")
	now := time.Now().UTC()
	targetOne := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1", AuthorizationRevision: "revision-1", TokenID: "token-1"}
	targetTwo := targetOne
	targetTwo.TokenID = "token-2"
	other := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-2", AuthorizationRevision: "revision-1", TokenID: "token-3"}
	for _, key := range []identityprincipal.CacheKey{targetOne, targetTwo, other} {
		if err := cache.Set(t.Context(), key, cacheTestEntry(now.Add(5*time.Minute)), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.Invalidate(t.Context(), "user-1", "workspace-1"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []identityprincipal.CacheKey{targetOne, targetTwo} {
		if _, found, err := cache.Get(t.Context(), key, now); err != nil || found {
			t.Fatalf("target key remained found=%v err=%v", found, err)
		}
	}
	if _, found, err := cache.Get(t.Context(), other, now); err != nil || !found {
		t.Fatalf("other subject was removed found=%v err=%v", found, err)
	}
}

func TestCacheRejectsEntriesWithoutAccessBundle(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := New(client, "")
	now := time.Now().UTC()
	err := cache.Set(t.Context(), identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1"}, identityprincipal.CacheEntry{Principal: identity.Principal{Known: true}, ExpiresAt: now.Add(time.Minute)}, now)
	if err == nil || !strings.Contains(err.Error(), "AccessBundle") {
		t.Fatalf("error=%v", err)
	}
}

func cacheTestEntry(expiresAt time.Time) identityprincipal.CacheEntry {
	bundle := identity.AccessBundle{
		ContractVersion:       identity.CurrentPolicyBundleVersion,
		AuthorizationRevision: "revision-1",
		ExpiresAt:             expiresAt,
		Subject:               identity.Subject{WorkspaceID: "workspace-1", SubjectID: "user-1"},
		FunctionGrants:        []identity.FunctionGrant{{Resource: "orders", Action: "read", Effect: identity.EffectAllow}},
		DataPolicies:          []identity.DataPolicy{{Key: "orders.read", Resource: "orders", Action: "read", Effect: identity.EffectAllow, DataScopes: []identity.DataScope{identity.DataScopeAll}}},
	}
	return identityprincipal.CacheEntry{Principal: identity.Principal{
		ContractVersion:       identity.PrincipalContextContractVersion,
		Known:                 true,
		WorkspaceID:           "workspace-1",
		UserID:                "user-1",
		AuthorizationRevision: "revision-1",
		AccessBundle:          &bundle,
	}, ExpiresAt: expiresAt}
}
