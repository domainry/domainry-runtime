package ratelimit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

// RateLimiter persists shared rate-limit decisions.
type RateLimiter struct {
	store *database.RuntimeStore
	db    *sql.DB
	now   func() time.Time
}

func NewRateLimiter(store *database.RuntimeStore) *RateLimiter {
	return &RateLimiter{store: store, db: store.DB(), now: time.Now}
}

func (l *RateLimiter) EnsureSchema(ctx context.Context) error {
	exists, err := l.store.RuntimeTableExists(ctx, "runtime_rate_limit_bucket")
	if err != nil {
		return fmt.Errorf("inspect runtime rate-limit schema: %w", err)
	}
	if !exists {
		return fmt.Errorf("runtime rate-limit schema is not materialized")
	}
	return nil
}

func (l *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	if err := ctx.Err(); err != nil {
		return ratelimit.Decision{}, err
	}
	if limit <= 0 || window <= 0 {
		return ratelimit.Decision{Allowed: true, Limit: limit}, nil
	}
	key = strings.TrimSpace(key)
	now := l.now().UTC()
	for attempt := 0; attempt < 3; attempt++ {
		decision, retry, err := l.allowOnce(ctx, key, limit, window, now)
		if !retry {
			return decision, err
		}
		if err := ctx.Err(); err != nil {
			return ratelimit.Decision{}, err
		}
	}
	return ratelimit.Decision{}, fmt.Errorf("rate limit bucket conflict")
}

func (l *RateLimiter) allowOnce(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (ratelimit.Decision, bool, error) {
	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ratelimit.Decision{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	selectBuilder := ormbuilder.NewSelectBuilder(l.store.SQLRenderer, "runtime_rate_limit_bucket").
		Columns("window_start_ns", "request_count").Where(ormbuilder.Equal("bucket_key", key))
	selectBuilder, err = l.store.Engine.ApplyClaimLock(selectBuilder, false)
	if err != nil {
		return ratelimit.Decision{}, false, fmt.Errorf("apply rate limit claim lock: %w", err)
	}
	query, args, err := selectBuilder.Build()
	if err != nil {
		return ratelimit.Decision{}, false, fmt.Errorf("build rate limit bucket query: %w", err)
	}
	var startNS int64
	var count int
	err = tx.QueryRowContext(ctx, query, args...).Scan(&startNS, &count)
	if err != nil && err != sql.ErrNoRows {
		return ratelimit.Decision{}, false, err
	}
	windowStart := time.Unix(0, startNS).UTC()
	if err == sql.ErrNoRows || startNS == 0 || now.Sub(windowStart) >= window {
		windowStart, count = now, 0
	}
	count++
	if err == sql.ErrNoRows {
		insert, insertArgs, buildErr := ormbuilder.NewInsertBuilder(l.store.SQLRenderer, "runtime_rate_limit_bucket").
			Columns("bucket_key", "window_start_ns", "request_count", "updated_at_ns").
			Values(key, windowStart.UnixNano(), count, now.UnixNano()).Build()
		if buildErr != nil {
			return ratelimit.Decision{}, false, fmt.Errorf("build rate limit bucket insert: %w", buildErr)
		}
		if _, insertErr := tx.ExecContext(ctx, insert, insertArgs...); insertErr != nil {
			return ratelimit.Decision{}, true, nil
		}
	} else {
		update, updateArgs, buildErr := ormbuilder.NewUpdateBuilder(l.store.SQLRenderer, "runtime_rate_limit_bucket").
			Set("window_start_ns", windowStart.UnixNano()).Set("request_count", count).Set("updated_at_ns", now.UnixNano()).
			Where(ormbuilder.Equal("bucket_key", key)).Build()
		if buildErr != nil {
			return ratelimit.Decision{}, false, fmt.Errorf("build rate limit bucket update: %w", buildErr)
		}
		if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
			return ratelimit.Decision{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ratelimit.Decision{}, true, nil
	}
	decision := ratelimit.Decision{Allowed: count <= limit, Count: count, Limit: limit}
	if !decision.Allowed {
		decision.RetryAfter = window - now.Sub(windowStart)
		if decision.RetryAfter < 0 {
			decision.RetryAfter = 0
		}
	}
	return decision, false, nil
}

var _ ratelimit.Limiter = (*RateLimiter)(nil)
