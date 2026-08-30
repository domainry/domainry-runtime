package ratelimitredis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	redis "github.com/redis/go-redis/v9"
)

const defaultPrefix = "domainry:ratelimit:v1:"

var fixedWindowScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {count, ttl}
`)

// RateLimiter uses one Redis key per fixed-window bucket. Redis owns expiry,
// so this backend does not write runtime_rate_limit_bucket.
type RateLimiter struct {
	client redis.UniversalClient
	prefix string
}

func Open(ctx context.Context, rawURL, prefix string, connectTimeout time.Duration) (*RateLimiter, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse rate-limit Redis URL: %w", err)
	}
	if connectTimeout <= 0 {
		return nil, errors.New("rate-limit Redis connect timeout must be positive")
	}
	options.DialTimeout = connectTimeout
	options.ReadTimeout = connectTimeout
	options.WriteTimeout = connectTimeout
	client := redis.NewClient(options)
	limiter := New(client, prefix)
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect rate-limit Redis: %w", err)
	}
	return limiter, nil
}

func New(client redis.UniversalClient, prefix string) *RateLimiter {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = defaultPrefix
	}
	return &RateLimiter{client: client, prefix: prefix}
}

func (l *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	if err := ctx.Err(); err != nil {
		return ratelimit.Decision{}, err
	}
	if limit <= 0 || window <= 0 {
		return ratelimit.Decision{Allowed: true, Limit: limit}, nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ratelimit.Decision{}, errors.New("rate limit bucket key is required")
	}
	if l == nil || l.client == nil {
		return ratelimit.Decision{}, errors.New("rate limit Redis client is required")
	}
	windowMillis := window.Milliseconds()
	if windowMillis < 1 {
		windowMillis = 1
	}
	result, err := fixedWindowScript.Run(ctx, l.client, []string{l.redisKey(key)}, windowMillis).Int64Slice()
	if err != nil {
		return ratelimit.Decision{}, fmt.Errorf("update Redis rate limit bucket: %w", err)
	}
	if len(result) != 2 {
		return ratelimit.Decision{}, fmt.Errorf("invalid Redis rate limit result length %d", len(result))
	}
	count, ttlMillis := result[0], result[1]
	decision := ratelimit.Decision{Allowed: count <= int64(limit), Count: int(count), Limit: limit}
	if !decision.Allowed {
		if ttlMillis < 1 {
			ttlMillis = 1
		}
		decision.RetryAfter = time.Duration(ttlMillis) * time.Millisecond
	}
	return decision, nil
}

func (l *RateLimiter) redisKey(bucket string) string {
	digest := sha256.Sum256([]byte(bucket))
	return l.prefix + hex.EncodeToString(digest[:])
}

func (l *RateLimiter) Close() error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Close()
}

var _ ratelimit.Limiter = (*RateLimiter)(nil)
