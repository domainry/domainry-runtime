// Package principalredis stores bounded Identity principal snapshots in Redis.
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
	redis "github.com/redis/go-redis/v9"
)

const (
	DefaultPrefix        = "domainry:identity:principal:v1:"
	cacheContractVersion = "domainry-principal-cache-v1"
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

type cacheEnvelope struct {
	ContractVersion string                 `json:"contract_version"`
	ExpiresAt       time.Time              `json:"expires_at"`
	Principal       identity.Principal     `json:"principal"`
	AccessBundle    *identity.AccessBundle `json:"access_bundle"`
}

type credentialLookup struct {
	ContractVersion string `json:"contract_version"`
	EntryKey        string `json:"entry_key"`
	SubjectKey      string `json:"subject_key"`
}

// Cache stores only non-secret Principal and AccessBundle state. Redis owns
// physical expiration while Resolver still enforces the signed token and
// logical snapshot expiry on every read.
type Cache struct {
	client redis.UniversalClient
	prefix string
}

func Open(ctx context.Context, rawURL, prefix string, connectTimeout time.Duration, cluster ...bool) (*Cache, error) {
	if connectTimeout <= 0 {
		return nil, errors.New("principal-cache Redis connect timeout must be positive")
	}
	var client redis.UniversalClient
	if len(cluster) > 0 && cluster[0] {
		options, err := redis.ParseClusterURL(strings.TrimSpace(rawURL))
		if err != nil {
			return nil, fmt.Errorf("parse principal-cache Redis cluster URL: %w", err)
		}
		options.DialTimeout = connectTimeout
		options.ReadTimeout = connectTimeout
		options.WriteTimeout = connectTimeout
		client = redis.NewClusterClient(options)
	} else {
		options, err := redis.ParseURL(strings.TrimSpace(rawURL))
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
		_ = client.Close()
		return nil, fmt.Errorf("connect principal-cache Redis: %w", err)
	}
	return cache, nil
}

func New(client redis.UniversalClient, prefix string) *Cache {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return &Cache{client: client, prefix: prefix}
}

func (cache *Cache) Get(ctx context.Context, key identityprincipal.CacheKey, now time.Time) (identityprincipal.CacheEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if cache == nil || cache.client == nil {
		return identityprincipal.CacheEntry{}, false, errors.New("principal-cache Redis client is required")
	}
	return cache.readEntry(ctx, cache.entryKey(key), now)
}

func (cache *Cache) readEntry(ctx context.Context, redisKey string, now time.Time) (identityprincipal.CacheEntry, bool, error) {
	encoded, err := cache.client.Get(ctx, redisKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return identityprincipal.CacheEntry{}, false, nil
	}
	if err != nil {
		return identityprincipal.CacheEntry{}, false, fmt.Errorf("read Redis principal cache: %w", err)
	}
	var envelope cacheEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		_ = cache.client.Del(ctx, redisKey).Err()
		return identityprincipal.CacheEntry{}, false, fmt.Errorf("decode Redis principal cache: %w", err)
	}
	if envelope.ContractVersion != cacheContractVersion || envelope.AccessBundle == nil || !now.Before(envelope.ExpiresAt) {
		_ = cache.client.Del(ctx, redisKey).Err()
		return identityprincipal.CacheEntry{}, false, nil
	}
	envelope.Principal.AccessBundle = envelope.AccessBundle
	return identityprincipal.CacheEntry{Principal: envelope.Principal, ExpiresAt: envelope.ExpiresAt}, true, nil
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
	ttl := entry.ExpiresAt.Sub(now)
	if ttl <= 0 {
		return cache.Delete(ctx, key)
	}
	envelope := cacheEnvelope{
		ContractVersion: cacheContractVersion,
		ExpiresAt:       entry.ExpiresAt,
		Principal:       entry.Principal,
		AccessBundle:    entry.Principal.AccessBundle,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode Redis principal cache: %w", err)
	}
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	if err := setEntryScript.Run(ctx, cache.client, []string{cache.entryKey(key), cache.subjectKey(key.SubjectID, key.WorkspaceID)}, encoded, ttlMillis).Err(); err != nil {
		return fmt.Errorf("write Redis principal cache: %w", err)
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
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("delete Redis principal cache: %w", err)
	}
	return nil
}

func (cache *Cache) Invalidate(ctx context.Context, subject identity.SubjectID, workspace identity.WorkspaceID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cache == nil || cache.client == nil {
		return errors.New("principal-cache Redis client is required")
	}
	if err := invalidateSubjectScript.Run(ctx, cache.client, []string{cache.subjectKey(subject, workspace)}).Err(); err != nil {
		return fmt.Errorf("invalidate Redis principal cache: %w", err)
	}
	return nil
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
		return identityprincipal.CacheEntry{}, false, nil
	}
	if err != nil {
		return identityprincipal.CacheEntry{}, false, fmt.Errorf("read Redis credential-cache lookup: %w", err)
	}
	var lookup credentialLookup
	if err := json.Unmarshal(encoded, &lookup); err != nil || lookup.ContractVersion != cacheContractVersion || !strings.HasPrefix(lookup.EntryKey, cache.prefix) || !strings.HasPrefix(lookup.SubjectKey, cache.prefix) {
		_ = cache.client.Del(ctx, lookupKey).Err()
		if err != nil {
			return identityprincipal.CacheEntry{}, false, fmt.Errorf("decode Redis credential-cache lookup: %w", err)
		}
		return identityprincipal.CacheEntry{}, false, nil
	}
	entry, found, err := cache.readEntry(ctx, lookup.EntryKey, now)
	if err != nil {
		return identityprincipal.CacheEntry{}, false, err
	}
	if !found {
		_ = cache.client.Del(ctx, lookupKey).Err()
	}
	return entry, found, nil
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
	envelope := cacheEnvelope{ContractVersion: cacheContractVersion, ExpiresAt: entry.ExpiresAt, Principal: entry.Principal, AccessBundle: entry.Principal.AccessBundle}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode Redis credential cache: %w", err)
	}
	entryKey := cache.credentialEntryKey(key, subject, workspace)
	subjectKey := cache.subjectKey(subject, workspace)
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	if err := setEntryScript.Run(ctx, cache.client, []string{entryKey, subjectKey}, encoded, ttlMillis).Err(); err != nil {
		return fmt.Errorf("write Redis credential cache: %w", err)
	}
	lookup, err := json.Marshal(credentialLookup{ContractVersion: cacheContractVersion, EntryKey: entryKey, SubjectKey: subjectKey})
	if err != nil {
		return fmt.Errorf("encode Redis credential-cache lookup: %w", err)
	}
	if err := cache.client.Set(ctx, cache.credentialLookupKey(key), lookup, ttl).Err(); err != nil {
		return fmt.Errorf("write Redis credential-cache lookup: %w", err)
	}
	return nil
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
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Redis credential-cache lookup for deletion: %w", err)
	}
	var lookup credentialLookup
	if err := json.Unmarshal(encoded, &lookup); err != nil || lookup.ContractVersion != cacheContractVersion || !strings.HasPrefix(lookup.EntryKey, cache.prefix) || !strings.HasPrefix(lookup.SubjectKey, cache.prefix) {
		_ = cache.client.Del(ctx, lookupKey).Err()
		if err != nil {
			return fmt.Errorf("decode Redis credential-cache lookup for deletion: %w", err)
		}
		return nil
	}
	_, err = cache.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, lookupKey)
		pipe.Del(ctx, lookup.EntryKey)
		pipe.SRem(ctx, lookup.SubjectKey, lookup.EntryKey)
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete Redis credential cache: %w", err)
	}
	return nil
}

func (cache *Cache) entryKey(key identityprincipal.CacheKey) string {
	return cache.subjectPrefix(key.SubjectID, key.WorkspaceID) + "entry:" + digest(string(key.AuthorizationRevision), key.TokenID)
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
	if cache == nil || cache.client == nil {
		return nil
	}
	return cache.client.Close()
}

var _ identityprincipal.Cache = (*Cache)(nil)
var _ identityprincipal.CredentialCache = (*Cache)(nil)
