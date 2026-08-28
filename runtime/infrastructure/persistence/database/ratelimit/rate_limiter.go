package ratelimit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

// RateLimiter persists shared rate-limit decisions.
type RateLimiter struct {
	store  *database.RuntimeStore
	db     *sql.DB
	schema runtimeschema.SQLDatabase
	driver string
	now    func() time.Time
	ready  atomic.Bool
}

func NewRateLimiter(store *database.RuntimeStore) *RateLimiter {
	return &RateLimiter{store: store, db: store.DB(), schema: store.SchemaDB(), driver: store.Driver(), now: time.Now}
}

func (l *RateLimiter) EnsureSchema(ctx context.Context) error {
	if l.ready.Load() {
		return nil
	}
	if err := l.ensureTable(ctx); err != nil {
		return err
	}
	l.ready.Store(true)
	return nil
}

func (l *RateLimiter) ensureTable(ctx context.Context) error {
	keyType := "TEXT"
	if l.driver == "mysql" {
		keyType = "VARCHAR(255)"
	}
	query := "CREATE TABLE IF NOT EXISTS " + l.store.TableIdentifier("runtime_rate_limit_bucket") + " (" +
		l.store.Identifier("bucket_key") + " " + keyType + " PRIMARY KEY, " +
		l.store.Identifier("window_start_ns") + " BIGINT NOT NULL, " +
		l.store.Identifier("request_count") + " BIGINT NOT NULL, " +
		l.store.Identifier("updated_at_ns") + " BIGINT NOT NULL)"
	_, err := l.schema.ExecContext(ctx, query)
	return err
}

func (l *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	if err := ctx.Err(); err != nil {
		return ratelimit.Decision{}, err
	}
	if limit <= 0 || window <= 0 {
		return ratelimit.Decision{Allowed: true, Limit: limit}, nil
	}
	if err := l.EnsureSchema(ctx); err != nil {
		return ratelimit.Decision{}, fmt.Errorf("ensure rate limit table: %w", err)
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
	query := "SELECT " + l.store.Identifier("window_start_ns") + ", " + l.store.Identifier("request_count") + " FROM " + l.store.TableIdentifier("runtime_rate_limit_bucket") + " WHERE " + l.store.Identifier("bucket_key") + " = " + l.store.Placeholder(1)
	if l.driver != "sqlite" {
		query += " FOR UPDATE"
	}
	var startNS int64
	var count int
	err = tx.QueryRowContext(ctx, query, key).Scan(&startNS, &count)
	if err != nil && err != sql.ErrNoRows {
		return ratelimit.Decision{}, false, err
	}
	windowStart := time.Unix(0, startNS).UTC()
	if err == sql.ErrNoRows || startNS == 0 || now.Sub(windowStart) >= window {
		windowStart, count = now, 0
	}
	count++
	if err == sql.ErrNoRows {
		insert := "INSERT INTO " + l.store.TableIdentifier("runtime_rate_limit_bucket") + " (" + l.store.Identifier("bucket_key") + ", " + l.store.Identifier("window_start_ns") + ", " + l.store.Identifier("request_count") + ", " + l.store.Identifier("updated_at_ns") + ") VALUES (" + strings.Join([]string{l.store.Placeholder(1), l.store.Placeholder(2), l.store.Placeholder(3), l.store.Placeholder(4)}, ", ") + ")"
		if _, insertErr := tx.ExecContext(ctx, insert, key, windowStart.UnixNano(), count, now.UnixNano()); insertErr != nil {
			return ratelimit.Decision{}, true, nil
		}
	} else {
		update := "UPDATE " + l.store.TableIdentifier("runtime_rate_limit_bucket") + " SET " + l.store.Identifier("window_start_ns") + " = " + l.store.Placeholder(1) + ", " + l.store.Identifier("request_count") + " = " + l.store.Placeholder(2) + ", " + l.store.Identifier("updated_at_ns") + " = " + l.store.Placeholder(3) + " WHERE " + l.store.Identifier("bucket_key") + " = " + l.store.Placeholder(4)
		if _, err := tx.ExecContext(ctx, update, windowStart.UnixNano(), count, now.UnixNano(), key); err != nil {
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
