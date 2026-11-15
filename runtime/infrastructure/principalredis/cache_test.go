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

func TestCacheRoundTripKeepsAccessBundleLocalAndRedisMarkerBounded(t *testing.T) {
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
			for _, forbidden := range []string{"access-token", "workspace-1", "user-1", "orders", "access_bundle", "function_grants", "data_policies"} {
				if strings.Contains(value, forbidden) {
					t.Fatalf("Redis marker exposed principal or permission material %q: %q", forbidden, value)
				}
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

func TestCacheDoesNotSharePermissionBodiesAcrossProcesses(t *testing.T) {
	server := miniredis.RunT(t)
	firstClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	secondClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	first := New(firstClient, "test:principal:")
	second := New(secondClient, "test:principal:")
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1", AuthorizationRevision: "revision-1", ScopeRevision: "scope-1", TokenID: "token-1"}
	entry := cacheTestEntry(now.Add(5 * time.Minute))
	if err := first.Set(t.Context(), key, entry, now); err != nil {
		t.Fatal(err)
	}
	if _, found, err := second.Get(t.Context(), key, now); err != nil || found {
		t.Fatalf("a separate process reconstructed permission state from Redis: found=%v err=%v", found, err)
	}
	if err := second.Set(t.Context(), key, entry, now); err != nil {
		t.Fatal(err)
	}
	if resolved, found, err := second.Get(t.Context(), key, now); err != nil || !found || !resolved.Principal.HasPermission("orders.read") {
		t.Fatalf("second process local cache was not populated: found=%v err=%v resolved=%#v", found, err, resolved)
	}
	for _, redisKey := range server.Keys() {
		value, err := server.Get(redisKey)
		if err != nil {
			continue
		}
		for _, forbidden := range []string{"workspace-1", "user-1", "orders", "revision-1", "scope-1", "token-1", "function_grants", "data_policies"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("Redis value %q exposed shared permission material %q: %q", redisKey, forbidden, value)
			}
		}
	}
	if err := first.Invalidate(t.Context(), "user-1", "workspace-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := second.Get(t.Context(), key, now); err != nil || found {
		t.Fatalf("cross-process invalidation did not reject the local snapshot: found=%v err=%v", found, err)
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

func TestCredentialCacheRoundTripAndSubjectInvalidation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := New(client, "test:principal:")
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := identityprincipal.CredentialCacheKey(strings.Repeat("a", 64))
	entry := cacheTestEntry(now.Add(5 * time.Minute))
	if err := cache.SetCredential(t.Context(), key, entry, now); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := cache.GetCredential(t.Context(), key, now)
	if err != nil || !found || !resolved.Principal.HasPermission("orders.read") {
		t.Fatalf("resolved=%#v found=%v err=%v", resolved, found, err)
	}
	for _, redisKey := range server.Keys() {
		if strings.Contains(redisKey, string(key)) || strings.Contains(redisKey, "workspace-1") || strings.Contains(redisKey, "user-1") {
			t.Fatalf("Redis credential cache exposed raw key material in %q", redisKey)
		}
		value, err := server.Get(redisKey)
		if err != nil {
			continue
		}
		for _, forbidden := range []string{"workspace-1", "user-1", "orders", "function_grants", "data_policies"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("Redis credential marker exposed principal or permission material %q: %q", forbidden, value)
			}
		}
	}
	if err := cache.Invalidate(t.Context(), "user-1", "workspace-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := cache.GetCredential(t.Context(), key, now); err != nil || found {
		t.Fatalf("invalidated credential remained found=%v err=%v", found, err)
	}
}

func TestCacheKeySeparatesScopeRevisions(t *testing.T) {
	cache := New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}), "test:principal:")
	t.Cleanup(func() { _ = cache.Close() })
	first := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1", AuthorizationRevision: "revision-1", ScopeRevision: "scope-1", TokenID: "token-1"}
	second := first
	second.ScopeRevision = "scope-2"
	if cache.entryKey(first) == cache.entryKey(second) {
		t.Fatal("scope revisions collided in the Redis marker key")
	}
}

func TestCacheMultiKeyScriptsUseOneRedisClusterSlot(t *testing.T) {
	cache := New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}), "test:principal:")
	t.Cleanup(func() { _ = cache.Close() })
	key := identityprincipal.CacheKey{WorkspaceID: "workspace-1", SubjectID: "user-1", AuthorizationRevision: "revision-1", TokenID: "token-1"}
	entryKey := cache.entryKey(key)
	subjectKey := cache.subjectKey(key.SubjectID, key.WorkspaceID)
	entryTagStart, entryTagEnd := strings.IndexByte(entryKey, '{'), strings.IndexByte(entryKey, '}')
	subjectTagStart, subjectTagEnd := strings.IndexByte(subjectKey, '{'), strings.IndexByte(subjectKey, '}')
	if entryTagStart < 0 || entryTagEnd <= entryTagStart || subjectTagStart < 0 || subjectTagEnd <= subjectTagStart {
		t.Fatalf("cluster hash tags missing: entry=%q subject=%q", entryKey, subjectKey)
	}
	if entryKey[entryTagStart:entryTagEnd+1] != subjectKey[subjectTagStart:subjectTagEnd+1] {
		t.Fatalf("multi-key script crosses slots: entry=%q subject=%q", entryKey, subjectKey)
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
