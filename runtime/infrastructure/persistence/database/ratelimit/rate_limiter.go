package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ormdriver "github.com/domainry/domainry-orm/driver"
	ormbuilder "github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

// RateLimiter persists shared rate-limit decisions.
type RateLimiter struct {
	store *database.RuntimeStore
	db    *sql.DB
	now   func() time.Time
	begin func(context.Context, *sql.DB) (ormdriver.Transaction, error)
}

const databaseRetryAttempts = 32

func NewRateLimiter(store *database.RuntimeStore) *RateLimiter {
	return &RateLimiter{store: store, db: store.DB(), now: time.Now, begin: store.Engine.BeginWrite}
}

func (l *RateLimiter) EnsureSchema(ctx context.Context) error {
	exists, err := l.store.RuntimeTableExists(ctx, "_rate_limit_buckets")
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
	if key == "" {
		return ratelimit.Decision{}, errors.New("rate limit bucket key is required")
	}
	if len(key) > 255 {
		return ratelimit.Decision{}, errors.New("rate limit bucket key exceeds 255 bytes")
	}
	var lastErr error
	for attempt := 0; attempt < databaseRetryAttempts; attempt++ {
		decision, retry, err := l.allowOnce(ctx, key, limit, window)
		if !retry {
			return decision, err
		}
		lastErr = err
		if err := ctx.Err(); err != nil {
			return ratelimit.Decision{}, err
		}
		if attempt < databaseRetryAttempts-1 {
			delayAttempt := min(attempt, 5)
			timer := time.NewTimer(time.Duration(1<<delayAttempt) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ratelimit.Decision{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return ratelimit.Decision{}, fmt.Errorf("rate limit bucket conflict after retries: %w", lastErr)
}

func (l *RateLimiter) allowOnce(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, bool, error) {
	tx, err := l.begin(ctx, l.db)
	if err != nil {
		return ratelimit.Decision{}, l.retryable(err), err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	selectBuilder := ormbuilder.NewSelectBuilder(l.store.SQLRenderer, "_rate_limit_buckets").
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
		return ratelimit.Decision{}, l.retryable(err), err
	}
	// Read the clock only after the claim lock is acquired. A timestamp captured
	// before waiting for the transaction can be older than the preceding writer's
	// window start and incorrectly reset the bucket under contention.
	now := l.now().UTC()
	previousStartNS, previousCount := startNS, count
	windowStart := time.Unix(0, startNS).UTC()
	if err == sql.ErrNoRows || startNS == 0 || now.Before(windowStart) || now.Sub(windowStart) >= window {
		windowStart, count = now, 0
	}
	count++
	if err == sql.ErrNoRows {
		insert, insertArgs, buildErr := ormbuilder.NewInsertBuilder(l.store.SQLRenderer, "_rate_limit_buckets").
			Columns("bucket_key", "window_start_ns", "request_count", "updated_at_ns").
			Values(key, windowStart.UnixNano(), count, now.UnixNano()).Build()
		if buildErr != nil {
			return ratelimit.Decision{}, false, fmt.Errorf("build rate limit bucket insert: %w", buildErr)
		}
		if _, insertErr := tx.ExecContext(ctx, insert, insertArgs...); insertErr != nil {
			return ratelimit.Decision{}, l.retryable(insertErr), insertErr
		}
	} else {
		update, updateArgs, buildErr := ormbuilder.NewUpdateBuilder(l.store.SQLRenderer, "_rate_limit_buckets").
			Set("window_start_ns", windowStart.UnixNano()).Set("request_count", count).Set("updated_at_ns", now.UnixNano()).
			Where(ormbuilder.And(
				ormbuilder.Equal("bucket_key", key),
				ormbuilder.Equal("window_start_ns", previousStartNS),
				ormbuilder.Equal("request_count", previousCount),
			)).Build()
		if buildErr != nil {
			return ratelimit.Decision{}, false, fmt.Errorf("build rate limit bucket update: %w", buildErr)
		}
		result, err := tx.ExecContext(ctx, update, updateArgs...)
		if err != nil {
			return ratelimit.Decision{}, l.retryable(err), err
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return ratelimit.Decision{}, false, fmt.Errorf("read rate limit bucket update result: %w", rowsErr)
		}
		if updated != 1 {
			return ratelimit.Decision{}, true, fmt.Errorf("rate limit bucket changed concurrently")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ratelimit.Decision{}, l.retryable(err), err
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

func (l *RateLimiter) retryable(err error) bool {
	switch l.store.Engine.ClassifyError(err) {
	case ormdriver.ErrorConflict, ormdriver.ErrorSerialization, ormdriver.ErrorDeadlock, ormdriver.ErrorUnavailable:
		return true
	default:
		return false
	}
}

var _ ratelimit.Limiter = (*RateLimiter)(nil)
