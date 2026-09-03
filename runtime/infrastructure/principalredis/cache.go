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

// Cache stores only non-secret Principal and AccessBundle state. Redis owns
// physical expiration while Resolver still enforces the signed token and
// logical snapshot expiry on every read.
type Cache struct {
	client redis.UniversalClient
	prefix string
}

func Open(ctx context.Context, rawURL, prefix string, connectTimeout time.Duration) (*Cache, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse principal-cache Redis URL: %w", err)
	}
	if connectTimeout <= 0 {
		return nil, errors.New("principal-cache Redis connect timeout must be positive")
	}
	options.DialTimeout = connectTimeout
	options.ReadTimeout = connectTimeout
	options.WriteTimeout = connectTimeout
	client := redis.NewClient(options)
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
	redisKey := cache.entryKey(key)
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

func (cache *Cache) entryKey(key identityprincipal.CacheKey) string {
	return cache.prefix + "entry:" + digest(string(key.WorkspaceID), string(key.SubjectID), string(key.AuthorizationRevision), key.TokenID)
}

func (cache *Cache) subjectKey(subject identity.SubjectID, workspace identity.WorkspaceID) string {
	return cache.prefix + "subject:" + digest(string(workspace), string(subject))
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
