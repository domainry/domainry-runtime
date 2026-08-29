package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSQLiteDialectContract(t *testing.T) {
	dialect := Dialect{}
	if dialect.Name() != "sqlite" || dialect.SQLDriver() != "sqlite" || dialect.SQLDialect().Identifier("runtime_table") != `"runtime_table"` || dialect.SQLDialect().Placeholder(2) != "?" || !strings.Contains(dialect.SchemaMigrationSQL(), "_schema_migrations") {
		t.Fatalf("dialect identity contract failed")
	}
	for _, test := range []struct {
		cfg  config.Config
		want string
	}{
		{cfg: config.Config{DatabaseDSN: " file:runtime.db "}, want: "file:runtime.db?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29"},
		{cfg: config.Config{}, want: "../data/runtime.db?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29"},
		{cfg: config.Config{DBPath: " runtime.db "}, want: "runtime.db?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29"},
		{cfg: config.Config{DatabaseDSN: "file:runtime.db?cache=shared"}, want: "file:runtime.db?cache=shared&_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29"},
	} {
		got, err := dialect.DSN(test.cfg)
		if err != nil || got != test.want {
			t.Fatalf("dsn=%q err=%v want=%q", got, err, test.want)
		}
	}

	databasePath := filepath.Join(t.TempDir(), "nested", "runtime.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dialect.Configure(t.Context(), db, config.Config{DBPath: databasePath}); err != nil {
		t.Fatalf("path configure=%v", err)
	}
	if db.Stats().MaxOpenConnections != ormsqlite.DefaultMaxOpenConnections {
		t.Fatalf("configure stats=%#v", db.Stats())
	}
	if _, err := os.Stat(filepath.Dir(databasePath)); err != nil {
		t.Fatalf("database directory=%v", err)
	}
}

func TestSQLiteFileDatabaseAllowsReadWhileWriteTransactionOwnsConnection(t *testing.T) {
	dialect := Dialect{}
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "concurrent.db")}
	dsn, err := dialect.DSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(dialect.SQLDriver(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dialect.Configure(t.Context(), db, cfg); err != nil {
		t.Fatal(err)
	}
	if db.Stats().MaxOpenConnections != ormsqlite.DefaultMaxOpenConnections {
		t.Fatalf("max open connections=%d", db.Stats().MaxOpenConnections)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE concurrent_reads (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO concurrent_reads (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM concurrent_reads`).Scan(&count); err != nil {
		t.Fatalf("read behind write transaction blocked: %v", err)
	}
	if count != 0 {
		t.Fatalf("uncommitted row became visible: count=%d", count)
	}
}

func TestSQLiteConcurrentWriterContentionIsBounded(t *testing.T) {
	dialect := Dialect{}
	cfg := config.Config{
		DBPath: filepath.Join(t.TempDir(), "writer-contention.db"), DatabaseLockTimeout: 100 * time.Millisecond,
		DatabaseMaxOpenConns: 4, DatabaseMaxIdleConns: 2,
	}
	dsn, err := dialect.DSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(dialect.SQLDriver(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dialect.Configure(t.Context(), db, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE writer_contention (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO writer_contention (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `INSERT INTO writer_contention (id) VALUES (2)`); err == nil || (!strings.Contains(strings.ToLower(err.Error()), "locked") && !strings.Contains(strings.ToLower(err.Error()), "busy")) {
		t.Fatalf("concurrent writer error=%v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("concurrent writer waited for context deadline: %s", elapsed)
	}
}

func TestSQLiteCurrencyDivisionUsesExactMinorUnitsAndNullSafeDenominator(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var integerResult, decimalResult string
	var nullResult any
	if err := db.QueryRow(`SELECT runtime_currency_divide_minor(1010, 2), runtime_currency_divide_minor(3040, 2.5), runtime_currency_divide_minor(1010, NULLIF(0, 0))`).Scan(&integerResult, &decimalResult, &nullResult); err != nil {
		t.Fatal(err)
	}
	if integerResult != "505" || decimalResult != "1216" || nullResult != nil {
		t.Fatalf("integer=%q decimal=%q null=%#v", integerResult, decimalResult, nullResult)
	}
	if err := db.QueryRow(`SELECT runtime_currency_divide_minor(1010, 0)`).Scan(&integerResult); err == nil || !strings.Contains(err.Error(), "use NULLIF or CASE") {
		t.Fatalf("unguarded zero divisor err=%v", err)
	}
}

func TestSQLiteDialectConfigureFailures(t *testing.T) {
	dialect := Dialect{}
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dialect.Configure(t.Context(), db, config.Config{DBPath: filepath.Join(blocker, "runtime.db")}); err == nil || !strings.Contains(err.Error(), "create sqlite database directory") {
		t.Fatalf("directory error=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := dialect.Configure(ctx, db, config.Config{DBPath: filepath.Join(t.TempDir(), "cancelled.db")}); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "busy timeout") {
		t.Fatalf("busy timeout error=%v", err)
	}

	foreignDB := sql.OpenDB(sqliteFailureConnector{})
	defer foreignDB.Close()
	if err := dialect.Configure(t.Context(), foreignDB, config.Config{DBPath: filepath.Join(t.TempDir(), "failure.db")}); err == nil || !strings.Contains(err.Error(), "configure sqlite foreign keys") {
		t.Fatalf("foreign key error=%v", err)
	}
}

type sqliteFailureConnector struct{}

func (sqliteFailureConnector) Connect(context.Context) (driver.Conn, error) {
	return &sqliteFailureConn{}, nil
}
func (sqliteFailureConnector) Driver() driver.Driver { return sqliteFailureDriver{} }

type sqliteFailureDriver struct{}

func (sqliteFailureDriver) Open(string) (driver.Conn, error) { return &sqliteFailureConn{}, nil }

type sqliteFailureConn struct{ calls int }

func (*sqliteFailureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*sqliteFailureConn) Close() error                        { return nil }
func (*sqliteFailureConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *sqliteFailureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.calls++
	if c.calls == 2 {
		return nil, errors.New("foreign key pragma failed")
	}
	return driver.RowsAffected(0), nil
}
