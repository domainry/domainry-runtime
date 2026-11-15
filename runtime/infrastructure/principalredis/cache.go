// Package principalredis coordinates bounded process-local Identity principal
// caches through non-sensitive Redis expiry and invalidation markers.
package principalredis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	identity "github.com/domainry/domainry-identity-sdk/authorization"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/redisconnect"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/redisdiagnostics"
	redis "github.com/redis/go-redis/v9"
)

const (
	DefaultPrefix        = "domainry:identity:principal:v2:"
	cacheContractVersion = "domainry-principal-cache-marker-v2"
)

var setEntryScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SADD', KEYS[2], KEYS[1])
local current_ttl = redis.call('PTTL', KEYS[2])
local requested_ttl = tonumber(ARGV[2])
if current_ttl < requested_ttl then
  redis.call('PEXPIRE', KEYS[2], requested_ttl)
end
return 1
`)

var invalidateSubjectScript = redis.NewScript(`
local entries = redis.call('SMEMBERS', KEYS[1])
for _, entry in ipairs(entries) do
  redis.call('DEL', entry)
end
redis.call('DEL', KEYS[1])
return #entries
`)

type cacheMarker struct {
	ContractVersion string `json:"contract_version"`
	ExpiresAt       int64  `json:"expires_at"`
}

type credentialLookup struct {
	ContractVersion string `json:"contract_version"`
	EntryKey        string `json:"entry_key"`
	SubjectKey      string `json:"subject_key"`
}

// Cache keeps Principal and AccessBundle state only in process memory. Redis
// stores opaque hashes, expiry markers, and subject invalidation indexes so a
// replica can reject a stale local snapshot without persisting permission
// bodies, bearer credentials, cookies, or refresh tokens in shared hot state.
type Cache struct {
	client           redis.UniversalClient
	prefix           string
	local            identityprincipal.Cache
	localCredentials identityprincipal.CredentialCache
}

func Open(ctx context.Context, rawURL, prefix string, connectTimeout time.Duration, caCertFile string, cluster ...bool) (*Cache, error) {
	if connectTimeout <= 0 {
		return nil, errors.New("principal-cache Redis connect timeout must be positive")
	}
	var client redis.UniversalClient
	if len(cluster) > 0 && cluster[0] {
		options, err := redisconnect.ParseClusterURL(rawURL, caCertFile, "principal-cache")
		if err != nil {
			return nil, fmt.Errorf("parse principal-cache Redis cluster URL: %w", err)
		}
		options.DialTimeout = connectTimeout
		options.ReadTimeout = connectTimeout
		options.WriteTimeout = connectTimeout
		client = redis.NewClusterClient(options)
	} else {
		options, err := redisconnect.ParseURL(rawURL, caCertFile, "principal-cache")
		if err != nil {
			return nil, fmt.Errorf("parse principal-cache Redis URL: %w", err)
		}
		options.DialTimeout = connectTimeout
		options.ReadTimeout = connectTimeout
		options.WriteTimeout = connectTimeout
		client = redis.NewClient(options)
	}
	cache := New(client, prefix)
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = cache.Close()
		return nil, fmt.Errorf("connect principal-cache Redis: %w", err)
	}
	return cache, nil
}

func New(client redis.UniversalClient, prefix string) *Cache {
	redisdiagnostics.Install(client, redisdiagnostics.ComponentPrincipalCache)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultPrefix
	}
	local := identityprincipal.NewMemoryCache()
	localCredentials, ok := local.(identityprincipal.CredentialCache)
	if !ok {
		panic("identity principal memory cache must implement credential caching")
	}
	return &Cache{client: client, prefix: prefix, local: local, localCredentials: localCredentials}
}

func (cache *Cache) Get(ctx context.Context, key identityprincipal.CacheKey, now time.Time) (identityprincipal.CacheEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if cache == nil || cache.client == nil {
		return identityprincipal.CacheEntry{}, false, errors.New("principal-cache Redis client is required")
	}
	markerExpiry, found, err := cache.readMarker(ctx, cache.entryKey(key), now)
	if err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if !found {
		_ = cache.local.Delete(ctx, key)
		return identityprincipal.CacheEntry{}, false, nil
	}
	entry, found, err := cache.local.Get(ctx, key, now)
	if err != nil || !found {
		return entry, found, err
	}
	if entry.ExpiresAt.UTC().UnixMilli() != markerExpiry.UTC().UnixMilli() {
		_ = cache.local.Delete(ctx, key)
		return identityprincipal.CacheEntry{}, false, nil
	}
	return entry, true, nil
}

func (cache *Cache) readMarker(ctx context.Context, redisKey string, now time.Time) (time.Time, bool, error) {
	encoded, err := cache.client.Get(ctx, redisKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read Redis principal-cache marker: %w", err)
	}
	var marker cacheMarker
	if err := json.Unmarshal(encoded, &marker); err != nil {
		_ = cache.client.Del(ctx, redisKey).Err()
		return time.Time{}, false, fmt.Errorf("decode Redis principal-cache marker: %w", err)
	}
	if marker.ContractVersion != cacheContractVersion || now.UTC().UnixMilli() >= marker.ExpiresAt {
		_ = cache.client.Del(ctx, redisKey).Err()
		return time.Time{}, false, nil
	}
	return time.UnixMilli(marker.ExpiresAt).UTC(), true, nil
}

func (cache *Cache) Set(ctx context.Context, key identityprincipal.CacheKey, entry identityprincipal.CacheEntry, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	if entry.Principal.AccessBundle == nil {
		return errors.New("principal-cache AccessBundle is required")
	}
	if !entry.ExpiresAt.After(now) {
		return cache.Delete(ctx, key)
	}
	if err := cache.writeMarker(ctx, cache.entryKey(key), cache.subjectKey(key.SubjectID, key.WorkspaceID), entry.ExpiresAt, now); err != nil {
		return err
	}
	return cache.local.Set(ctx, key, entry, now)
}

func (cache *Cache) writeMarker(ctx context.Context, entryKey, subjectKey string, expiresAt, now time.Time) error {
	ttlMillis := expiresAt.Sub(now).Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	encoded, err := json.Marshal(cacheMarker{ContractVersion: cacheContractVersion, ExpiresAt: expiresAt.UTC().UnixMilli()})
	if err != nil {
		return fmt.Errorf("encode Redis principal-cache marker: %w", err)
	}
	if err := setEntryScript.Run(ctx, cache.client, []string{entryKey, subjectKey}, encoded, ttlMillis).Err(); err != nil {
		return fmt.Errorf("write Redis principal-cache marker: %w", err)
	}
	return nil
}

func (cache *Cache) Delete(ctx context.Context, key identityprincipal.CacheKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	entryKey := cache.entryKey(key)
	pipe := cache.client.TxPipeline()
	pipe.Del(ctx, entryKey)
	pipe.SRem(ctx, cache.subjectKey(key.SubjectID, key.WorkspaceID), entryKey)
	_, redisErr := pipe.Exec(ctx)
	localErr := cache.local.Delete(ctx, key)
	if redisErr != nil {
		redisErr = fmt.Errorf("delete Redis principal-cache marker: %w", redisErr)
	}
	return errors.Join(redisErr, localErr)
}

func (cache *Cache) Invalidate(ctx context.Context, subject identity.SubjectID, workspace identity.WorkspaceID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	redisErr := invalidateSubjectScript.Run(ctx, cache.client, []string{cache.subjectKey(subject, workspace)}).Err()
	localErr := cache.local.Invalidate(ctx, subject, workspace)
	if redisErr != nil {
		redisErr = fmt.Errorf("invalidate Redis principal-cache markers: %w", redisErr)
	}
	return errors.Join(redisErr, localErr)
}

func (cache *Cache) GetCredential(ctx context.Context, key identityprincipal.CredentialCacheKey, now time.Time) (identityprincipal.CacheEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if cache == nil || cache.client == nil {
		return identityprincipal.CacheEntry{}, false, errors.New("principal-cache Redis client is required")
	}
	lookupKey := cache.credentialLookupKey(key)
	encoded, err := cache.client.Get(ctx, lookupKey).Bytes()
	if errors.Is(err, redis.Nil) {
		_ = cache.localCredentials.DeleteCredential(ctx, key)
		return identityprincipal.CacheEntry{}, false, nil
	}
	if err != nil {
		return identityprincipal.CacheEntry{}, false, fmt.Errorf("read Redis credential-cache lookup: %w", err)
	}
	lookup, err := cache.decodeCredentialLookup(encoded)
	if err != nil {
		_ = cache.client.Del(ctx, lookupKey).Err()
		_ = cache.localCredentials.DeleteCredential(ctx, key)
		return identityprincipal.CacheEntry{}, false, err
	}
	markerExpiry, found, err := cache.readMarker(ctx, lookup.EntryKey, now)
	if err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if !found {
		_ = cache.client.Del(ctx, lookupKey).Err()
		_ = cache.localCredentials.DeleteCredential(ctx, key)
		return identityprincipal.CacheEntry{}, false, nil
	}
	entry, found, err := cache.localCredentials.GetCredential(ctx, key, now)
	if err != nil || !found {
		return entry, found, err
	}
	if entry.ExpiresAt.UTC().UnixMilli() != markerExpiry.UTC().UnixMilli() {
		_ = cache.localCredentials.DeleteCredential(ctx, key)
		return identityprincipal.CacheEntry{}, false, nil
	}
	return entry, true, nil
}

func (cache *Cache) SetCredential(ctx context.Context, key identityprincipal.CredentialCacheKey, entry identityprincipal.CacheEntry, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	if entry.Principal.AccessBundle == nil {
		return errors.New("principal-cache AccessBundle is required")
	}
	workspace := identity.WorkspaceID(entry.Principal.WorkspaceID)
	subject := identity.SubjectID(entry.Principal.UserID)
	if !workspace.Valid() || !subject.Valid() {
		return errors.New("principal-cache credential subject is required")
	}
	ttl := entry.ExpiresAt.Sub(now)
	if ttl <= 0 {
		return cache.DeleteCredential(ctx, key)
	}
	entryKey := cache.credentialEntryKey(key, subject, workspace)
	subjectKey := cache.subjectKey(subject, workspace)
	if err := cache.writeMarker(ctx, entryKey, subjectKey, entry.ExpiresAt, now); err != nil {
		return err
	}
	lookup, err := json.Marshal(credentialLookup{ContractVersion: cacheContractVersion, EntryKey: entryKey, SubjectKey: subjectKey})
	if err != nil {
		return fmt.Errorf("encode Redis credential-cache lookup: %w", err)
	}
	if err := cache.client.Set(ctx, cache.credentialLookupKey(key), lookup, ttl).Err(); err != nil {
		return fmt.Errorf("write Redis credential-cache lookup: %w", err)
	}
	return cache.localCredentials.SetCredential(ctx, key, entry, now)
}

func (cache *Cache) DeleteCredential(ctx context.Context, key identityprincipal.CredentialCacheKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	lookupKey := cache.credentialLookupKey(key)
	encoded, err := cache.client.Get(ctx, lookupKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return cache.localCredentials.DeleteCredential(ctx, key)
	}
	if err != nil {
		return fmt.Errorf("read Redis credential-cache lookup for deletion: %w", err)
	}
	lookup, decodeErr := cache.decodeCredentialLookup(encoded)
	if decodeErr != nil {
		redisErr := cache.client.Del(ctx, lookupKey).Err()
		localErr := cache.localCredentials.DeleteCredential(ctx, key)
		return errors.Join(decodeErr, redisErr, localErr)
	}
	_, redisErr := cache.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, lookupKey)
		pipe.Del(ctx, lookup.EntryKey)
		pipe.SRem(ctx, lookup.SubjectKey, lookup.EntryKey)
		return nil
	})
	localErr := cache.localCredentials.DeleteCredential(ctx, key)
	if redisErr != nil {
		redisErr = fmt.Errorf("delete Redis credential-cache marker: %w", redisErr)
	}
	return errors.Join(redisErr, localErr)
}

func (cache *Cache) decodeCredentialLookup(encoded []byte) (credentialLookup, error) {
	var lookup credentialLookup
	if err := json.Unmarshal(encoded, &lookup); err != nil {
		return credentialLookup{}, fmt.Errorf("decode Redis credential-cache lookup: %w", err)
	}
	if lookup.ContractVersion != cacheContractVersion ||
		!strings.HasPrefix(lookup.EntryKey, cache.prefix) ||
		!strings.HasPrefix(lookup.SubjectKey, cache.prefix) {
		return credentialLookup{}, errors.New("decode Redis credential-cache lookup: invalid contract or key scope")
	}
	return lookup, nil
}

func (cache *Cache) entryKey(key identityprincipal.CacheKey) string {
	return cache.subjectPrefix(key.SubjectID, key.WorkspaceID) + "entry:" + digest(string(key.AuthorizationRevision), key.ScopeRevision, key.TokenID)
}

func (cache *Cache) subjectKey(subject identity.SubjectID, workspace identity.WorkspaceID) string {
	return cache.subjectPrefix(subject, workspace) + "subject"
}

func (cache *Cache) credentialLookupKey(key identityprincipal.CredentialCacheKey) string {
	return cache.prefix + "credential-lookup:" + digest(string(key))
}

func (cache *Cache) credentialEntryKey(key identityprincipal.CredentialCacheKey, subject identity.SubjectID, workspace identity.WorkspaceID) string {
	return cache.subjectPrefix(subject, workspace) + "credential:" + digest(string(key))
}

func (cache *Cache) subjectPrefix(subject identity.SubjectID, workspace identity.WorkspaceID) string {
	tag := digest(string(workspace), string(subject))
	return cache.prefix + "{" + tag + "}:"
}

func digest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (cache *Cache) Close() error {
	if cache == nil {
		return nil
	}
	var localErr error
	if closer, ok := cache.local.(interface{ Close() error }); ok {
		localErr = closer.Close()
	}
	var redisErr error
	if cache.client != nil {
		redisErr = cache.client.Close()
	}
	return errors.Join(localErr, redisErr)
}

var _ identityprincipal.Cache = (*Cache)(nil)
var _ identityprincipal.CredentialCache = (*Cache)(nil)
