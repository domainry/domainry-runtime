package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/jackc/pgx/v5/pgconn"
)

type postgresDialectConnector struct{ pingErr error }

func (c postgresDialectConnector) Connect(context.Context) (driver.Conn, error) {
	return postgresDialectConnection{pingErr: c.pingErr}, nil
}
func (postgresDialectConnector) Driver() driver.Driver { return postgresDialectDriver{} }

type postgresDialectDriver struct{}

func (postgresDialectDriver) Open(string) (driver.Conn, error) { return nil, io.EOF }

type postgresDialectConnection struct{ pingErr error }

func (postgresDialectConnection) Prepare(string) (driver.Stmt, error) { return nil, io.EOF }
func (postgresDialectConnection) Close() error                        { return nil }
func (postgresDialectConnection) Begin() (driver.Tx, error)           { return nil, io.EOF }
func (c postgresDialectConnection) Ping(context.Context) error        { return c.pingErr }

func TestDialectContract(t *testing.T) {
	dialect := Dialect{}
	if dialect.Name() != "postgres" || dialect.SQLDriver() != "pgx" {
		t.Fatalf("name=%q driver=%q", dialect.Name(), dialect.SQLDriver())
	}
	if _, err := dialect.DSN(config.Config{}); err == nil {
		t.Fatal("missing DSN accepted")
	}
	if dsn, err := dialect.DSN(config.Config{DatabaseDSN: " postgres://runtime "}); err != nil || dsn != "postgres://runtime" {
		t.Fatalf("dsn=%q err=%v", dsn, err)
	}
	if got := dialect.SQLDialect().Identifier("order_item"); got != `"order_item"` {
		t.Fatalf("identifier=%q", got)
	}
	assertPostgresIdentifierPanics(t, func() { dialect.SQLDialect().Identifier(`order"item`) })
	if got := dialect.SQLDialect().Placeholder(7); got != "$7" {
		t.Fatalf("placeholder=%q", got)
	}
	if sql := dialect.SchemaMigrationSQL(); !strings.Contains(sql, `"_schema_migrations"`) || !strings.Contains(sql, "PRIMARY KEY") {
		t.Fatalf("migration SQL=%q", sql)
	}
}

func assertPostgresIdentifierPanics(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("unsafe SQL identifier accepted")
		}
	}()
	run()
}

func TestDialectConfigurePingsDatabase(t *testing.T) {
	dialect := Dialect{}
	for _, test := range []struct {
		name    string
		pingErr error
		wantErr bool
	}{
		{name: "success"},
		{name: "failure", pingErr: &pgconn.PgError{Code: "28P01", Message: "authentication failed"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(postgresDialectConnector{pingErr: test.pingErr})
			t.Cleanup(func() { _ = db.Close() })
			err := dialect.Configure(t.Context(), db, config.Config{})
			if (err != nil) != test.wantErr || test.wantErr && !strings.Contains(err.Error(), "connect postgres database (authentication)") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
