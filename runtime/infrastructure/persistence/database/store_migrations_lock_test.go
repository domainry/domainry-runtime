package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

var (
	migrationLockDriverOnce sync.Once
	migrationLockSequence   atomic.Uint64
	migrationLockScripts    sync.Map
)

type migrationLockScript struct {
	mu             sync.Mutex
	openErr        error
	queryErr       error
	results        []driver.Value
	queries        []string
	execs          []string
	execContextErr error
}

type migrationLockDriver struct{}

func (migrationLockDriver) Open(name string) (driver.Conn, error) {
	value, ok := migrationLockScripts.Load(name)
	if !ok {
		return nil, errors.New("migration lock script not found")
	}
	script := value.(*migrationLockScript)
	if script.openErr != nil {
		return nil, script.openErr
	}
	return &migrationLockConn{script: script}, nil
}

type migrationLockConn struct{ script *migrationLockScript }

func (*migrationLockConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}
func (*migrationLockConn) Close() error              { return nil }
func (*migrationLockConn) Begin() (driver.Tx, error) { return nil, errors.New("begin not supported") }

func (conn *migrationLockConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	conn.script.mu.Lock()
	defer conn.script.mu.Unlock()
	conn.script.queries = append(conn.script.queries, query)
	if conn.script.queryErr != nil {
		return nil, conn.script.queryErr
	}
	result := driver.Value(false)
	if len(conn.script.results) > 0 {
		result = conn.script.results[0]
		conn.script.results = conn.script.results[1:]
	}
	return &migrationLockRows{value: result}, nil
}

func (conn *migrationLockConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	conn.script.mu.Lock()
	defer conn.script.mu.Unlock()
	conn.script.execs = append(conn.script.execs, query)
	conn.script.execContextErr = ctx.Err()
	return driver.RowsAffected(1), nil
}

type migrationLockRows struct {
	value driver.Value
	done  bool
}

func (*migrationLockRows) Columns() []string { return []string{"locked"} }
func (*migrationLockRows) Close() error      { return nil }
func (rows *migrationLockRows) Next(dest []driver.Value) error {
	if rows.done {
		return io.EOF
	}
	rows.done = true
	dest[0] = rows.value
	return nil
}

func openMigrationLockScript(t *testing.T, script *migrationLockScript) *sql.DB {
	t.Helper()
	migrationLockDriverOnce.Do(func() { sql.Register("runtime-migration-lock-script", migrationLockDriver{}) })
	name := "migration-lock-" + strings.TrimSpace(time.Now().Format("150405.000000000")) + "-" + strconv.FormatUint(migrationLockSequence.Add(1), 10)
	migrationLockScripts.Store(name, script)
	t.Cleanup(func() { migrationLockScripts.Delete(name) })
	db, err := sql.Open("runtime-migration-lock-script", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAcquireMigrationLockCoversExternalDialectsAndRelease(t *testing.T) {
	tests := []struct {
		name      string
		dialect   dialect
		result    driver.Value
		lockSQL   string
		unlockSQL string
	}{
		{name: "postgres", dialect: postgres.Dialect{}, result: true, lockSQL: "pg_try_advisory_lock", unlockSQL: "pg_advisory_unlock"},
		{name: "mysql", dialect: mysql.Dialect{}, result: int64(1), lockSQL: "GET_LOCK", unlockSQL: "RELEASE_LOCK"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &migrationLockScript{results: []driver.Value{test.result}}
			db := openMigrationLockScript(t, script)
			store := &RuntimeStore{db: db, dialect: test.dialect, databaseSchema: "runtime", operationalMetrics: NewRuntimeOperationalMetrics("", "")}
			ctx, cancel := context.WithCancel(t.Context())
			release, err := store.acquireMigrationLock(ctx, config.Config{MigrationInstanceID: "test-instance"})
			if err != nil {
				t.Fatal(err)
			}
			if store.migrationConn == nil {
				t.Fatal("migration connection was not retained while lock is held")
			}
			cancel()
			release()
			script.mu.Lock()
			defer script.mu.Unlock()
			if len(script.queries) != 1 || !strings.Contains(script.queries[0], test.lockSQL) {
				t.Fatalf("lock queries=%v", script.queries)
			}
			if len(script.execs) != 1 || !strings.Contains(script.execs[0], test.unlockSQL) {
				t.Fatalf("unlock execs=%v", script.execs)
			}
			if script.execContextErr != nil || store.migrationConn != nil {
				t.Fatalf("release context error=%v retained=%v", script.execContextErr, store.migrationConn != nil)
			}
		})
	}
	script := &migrationLockScript{results: []driver.Value{true}}
	migrationDB := openMigrationLockScript(t, script)
	store := &RuntimeStore{migrationDB: migrationDB, dialect: postgres.Dialect{}, config: config.Config{DatabaseConnectTimeout: time.Second}}
	release, err := store.acquireMigrationLock(t.Context(), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestAcquireMigrationLockReportsConnectionQueryAndTimeoutFailures(t *testing.T) {
	tests := []struct {
		name    string
		script  *migrationLockScript
		timeout time.Duration
		want    string
	}{
		{name: "connection", script: &migrationLockScript{openErr: errors.New("connect failed")}, want: "acquire migration connection"},
		{name: "query", script: &migrationLockScript{queryErr: errors.New("query failed")}, want: "acquire migration lock"},
		{name: "timeout", script: &migrationLockScript{results: []driver.Value{false}}, timeout: time.Millisecond, want: "migration.lock_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigrationLockScript(t, test.script)
			store := &RuntimeStore{db: db, dialect: postgres.Dialect{}, config: config.Config{DatabaseLockTimeout: test.timeout}}
			if _, err := store.acquireMigrationLock(t.Context(), config.Config{MigrationInstanceID: "test-instance"}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestAcquireMigrationLockWaitsThenSucceeds(t *testing.T) {
	script := &migrationLockScript{results: []driver.Value{false, true}}
	db := openMigrationLockScript(t, script)
	store := &RuntimeStore{db: db, dialect: postgres.Dialect{}, config: config.Config{DatabaseLockTimeout: time.Second}}
	release, err := store.acquireMigrationLock(t.Context(), config.Config{MigrationInstanceID: "wait-success"})
	if err != nil {
		t.Fatal(err)
	}
	release()
}
