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

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSQLiteDialectContract(t *testing.T) {
	dialect := Dialect{}
	if dialect.Name() != "sqlite" || dialect.SQLDriver() != "sqlite" || dialect.Identifier("runtime_table") != `"runtime_table"` || dialect.Placeholder(2) != "?" || !strings.Contains(dialect.SchemaMigrationSQL(), "_schema_migrations") {
		t.Fatalf("dialect identity contract failed")
	}
	for _, test := range []struct {
		cfg  config.Config
		want string
	}{
		{cfg: config.Config{DatabaseDSN: " file:runtime.db "}, want: "file:runtime.db"},
		{cfg: config.Config{}, want: "../data/app.db"},
		{cfg: config.Config{DBPath: " runtime.db "}, want: "runtime.db"},
	} {
		got, err := dialect.DSN(test.cfg)
		if err != nil || got != test.want {
			t.Fatalf("dsn=%q err=%v want=%q", got, err, test.want)
		}
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := dialect.Configure(t.Context(), db, ":memory:"); err != nil || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("memory configure stats=%#v err=%v", db.Stats(), err)
	}
	if err := dialect.Configure(t.Context(), db, "file:runtime?mode=memory&cache=shared"); err != nil {
		t.Fatalf("file DSN configure=%v", err)
	}

	databasePath := filepath.Join(t.TempDir(), "nested", "runtime.db")
	if err := dialect.Configure(t.Context(), db, databasePath); err != nil {
		t.Fatalf("path configure=%v", err)
	}
	if _, err := os.Stat(filepath.Dir(databasePath)); err != nil {
		t.Fatalf("database directory=%v", err)
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
	if err := dialect.Configure(t.Context(), db, filepath.Join(blocker, "runtime.db")); err == nil || !strings.Contains(err.Error(), "create sqlite database directory") {
		t.Fatalf("directory error=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := dialect.Configure(ctx, db, ":memory:"); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "busy timeout") {
		t.Fatalf("busy timeout error=%v", err)
	}

	foreignDB := sql.OpenDB(sqliteFailureConnector{})
	defer foreignDB.Close()
	if err := dialect.Configure(t.Context(), foreignDB, ":memory:"); err == nil || !strings.Contains(err.Error(), "configure sqlite database") {
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
