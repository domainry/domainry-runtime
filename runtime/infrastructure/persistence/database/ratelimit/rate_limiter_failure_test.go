package ratelimit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRateLimiterInputSchemaAndRetryBoundaries(t *testing.T) {
	base := openRateLimitStore(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	limiter := NewRateLimiter(base)
	limiter.now = func() time.Time { return now }
	for _, input := range []struct {
		limit  int
		window time.Duration
	}{{limit: 0, window: time.Second}, {limit: 1, window: 0}} {
		decision, err := limiter.Allow(t.Context(), " key ", input.limit, input.window)
		if err != nil || !decision.Allowed || decision.Limit != input.limit {
			t.Fatalf("invalid limit/window decision=%#v err=%v", decision, err)
		}
	}

	wantErr := errors.New("injected rate limit failure")
	t.Run("mysql schema", func(t *testing.T) {
		state := &rateLimitDBState{execSteps: []rateLimitExecStep{{rows: 1}}}
		candidate, closeDB := scriptedRateLimiter(t, base, state)
		defer closeDB()
		candidate.store.Engine = mysql.Dialect{}
		candidate.store.SQLRenderer = mysql.Dialect{}.SQLDialect().WithSchema(candidate.store.SQLStore.DatabaseSchema)
		if err := candidate.EnsureSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(state.queries) != 1 || !strings.Contains(state.queries[0], "VARCHAR(255)") {
			t.Fatalf("schema query=%v", state.queries)
		}
	})

	t.Run("schema failure", func(t *testing.T) {
		candidate, closeDB := scriptedRateLimiter(t, base, &rateLimitDBState{execSteps: []rateLimitExecStep{{err: wantErr}}})
		defer closeDB()
		if _, err := candidate.Allow(t.Context(), "key", 1, time.Second); err == nil || !strings.Contains(err.Error(), "ensure rate limit table") {
			t.Fatalf("schema error=%v", err)
		}
	})

	t.Run("bounded conflicts", func(t *testing.T) {
		state := &rateLimitDBState{
			execSteps:   []rateLimitExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
			querySteps:  []rateLimitQueryStep{{err: sql.ErrNoRows}, {err: sql.ErrNoRows}, {err: sql.ErrNoRows}},
			commitSteps: []rateLimitCommitStep{{err: wantErr}, {err: wantErr}, {err: wantErr}},
		}
		candidate, closeDB := scriptedRateLimiter(t, base, state)
		defer closeDB()
		candidate.now = func() time.Time { return now }
		if _, err := candidate.Allow(t.Context(), " key ", 1, time.Second); err == nil || err.Error() != "rate limit bucket conflict" {
			t.Fatalf("conflict error=%v", err)
		}
	})

	t.Run("cancel between retries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		state := &rateLimitDBState{
			execSteps:   []rateLimitExecStep{{rows: 1}, {rows: 1}},
			querySteps:  []rateLimitQueryStep{{err: sql.ErrNoRows}},
			commitSteps: []rateLimitCommitStep{{err: wantErr, hook: cancel}},
		}
		candidate, closeDB := scriptedRateLimiter(t, base, state)
		defer closeDB()
		candidate.now = func() time.Time { return now }
		if _, err := candidate.Allow(ctx, "key", 1, time.Second); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	})
}

func TestRateLimiterAllowOnceDatabaseStages(t *testing.T) {
	base := openRateLimitStore(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	wantErr := errors.New("injected rate limit failure")

	tests := []struct {
		name       string
		state      *rateLimitDBState
		driverName string
		limit      int
		window     time.Duration
		wantRetry  bool
		wantErr    error
		wantCount  int
		wantWait   time.Duration
		wantForUpd bool
	}{
		{name: "begin", state: &rateLimitDBState{beginErrors: []error{wantErr}}, limit: 1, window: time.Second, wantErr: wantErr},
		{name: "query", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{err: wantErr}}}, limit: 1, window: time.Second, wantErr: wantErr},
		{name: "scan", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{"bad", int64(1)}}}}, limit: 1, window: time.Second, wantErr: errAny},
		{name: "insert", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{err: sql.ErrNoRows}}, execSteps: []rateLimitExecStep{{err: wantErr}}}, limit: 1, window: time.Second, wantRetry: true},
		{name: "update", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{now.UnixNano(), int64(1)}}}, execSteps: []rateLimitExecStep{{err: wantErr}}}, limit: 2, window: time.Second, wantErr: wantErr},
		{name: "commit", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{now.UnixNano(), int64(1)}}}, execSteps: []rateLimitExecStep{{rows: 1}}, commitSteps: []rateLimitCommitStep{{err: wantErr}}}, limit: 2, window: time.Second, wantRetry: true},
		{name: "postgres expired", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{now.Add(-time.Second).UnixNano(), int64(9)}}}, execSteps: []rateLimitExecStep{{rows: 1}}}, driverName: "postgres", limit: 2, window: time.Second, wantCount: 1, wantForUpd: true},
		{name: "zero start", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{int64(0), int64(9)}}}, execSteps: []rateLimitExecStep{{rows: 1}}}, limit: 2, window: time.Second, wantCount: 1},
		{name: "negative retry after", state: &rateLimitDBState{querySteps: []rateLimitQueryStep{{values: []driver.Value{now.UnixNano(), int64(0)}}}, execSteps: []rateLimitExecStep{{rows: 1}}}, limit: 0, window: -time.Nanosecond, wantCount: 1, wantWait: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, closeDB := scriptedRateLimiter(t, base, test.state)
			defer closeDB()
			if test.driverName == "postgres" {
				candidate.store.Engine = postgres.Dialect{}
				candidate.store.SQLRenderer = postgres.Dialect{}.SQLDialect().WithSchema(candidate.store.SQLStore.DatabaseSchema)
			}
			decision, retry, err := candidate.allowOnce(t.Context(), "key", test.limit, test.window, now)
			if test.wantErr == errAny {
				if err == nil {
					t.Fatal("expected scan error")
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v want=%v", err, test.wantErr)
			}
			if retry != test.wantRetry || decision.Count != test.wantCount || decision.RetryAfter != test.wantWait {
				t.Fatalf("decision=%#v retry=%v", decision, retry)
			}
			if test.wantForUpd && (len(test.state.queries) == 0 || !strings.Contains(test.state.queries[0], "FOR UPDATE")) {
				t.Fatalf("queries=%v", test.state.queries)
			}
		})
	}
}

var errAny = errors.New("any error")

func openRateLimitStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func scriptedRateLimiter(t *testing.T, store *database.RuntimeStore, state *rateLimitDBState) (*RateLimiter, func()) {
	t.Helper()
	db := sql.OpenDB(rateLimitConnector{state: state})
	limiter := NewRateLimiter(store)
	limiter.db = db
	limiter.schema = db
	return limiter, func() { _ = db.Close() }
}

type rateLimitExecStep struct {
	rows int64
	err  error
}

type rateLimitQueryStep struct {
	values []driver.Value
	err    error
}

type rateLimitCommitStep struct {
	err  error
	hook func()
}

type rateLimitDBState struct {
	beginErrors []error
	execSteps   []rateLimitExecStep
	querySteps  []rateLimitQueryStep
	commitSteps []rateLimitCommitStep
	queries     []string
}

type rateLimitConnector struct{ state *rateLimitDBState }

func (c rateLimitConnector) Connect(context.Context) (driver.Conn, error) {
	return &rateLimitConn{state: c.state}, nil
}
func (rateLimitConnector) Driver() driver.Driver { return rateLimitDriver{} }

type rateLimitDriver struct{}

func (rateLimitDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type rateLimitConn struct{ state *rateLimitDBState }

func (*rateLimitConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*rateLimitConn) Close() error                        { return nil }
func (c *rateLimitConn) Begin() (driver.Tx, error)         { return c.begin() }
func (c *rateLimitConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.begin()
}
func (c *rateLimitConn) begin() (driver.Tx, error) {
	if len(c.state.beginErrors) > 0 {
		err := c.state.beginErrors[0]
		c.state.beginErrors = c.state.beginErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &rateLimitTx{state: c.state}, nil
}
func (c *rateLimitConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.queries = append(c.state.queries, query)
	if len(c.state.execSteps) == 0 {
		return driver.RowsAffected(0), nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return driver.RowsAffected(step.rows), step.err
}
func (c *rateLimitConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queries = append(c.state.queries, query)
	if len(c.state.querySteps) == 0 {
		return &rateLimitRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &rateLimitRows{values: step.values}, nil
}

type rateLimitTx struct{ state *rateLimitDBState }

func (tx *rateLimitTx) Commit() error {
	if len(tx.state.commitSteps) == 0 {
		return nil
	}
	step := tx.state.commitSteps[0]
	tx.state.commitSteps = tx.state.commitSteps[1:]
	if step.hook != nil {
		step.hook()
	}
	return step.err
}
func (*rateLimitTx) Rollback() error { return nil }

type rateLimitRows struct {
	values []driver.Value
	done   bool
}

func (*rateLimitRows) Columns() []string { return []string{"window_start_ns", "request_count"} }
func (*rateLimitRows) Close() error      { return nil }
func (rows *rateLimitRows) Next(values []driver.Value) error {
	if rows.done || rows.values == nil {
		return io.EOF
	}
	copy(values, rows.values)
	rows.done = true
	return nil
}
