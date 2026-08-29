package mysql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type mysqlDialectConnector struct{ pingErr error }

func (c mysqlDialectConnector) Connect(context.Context) (driver.Conn, error) {
	return mysqlDialectConnection{pingErr: c.pingErr}, nil
}
func (mysqlDialectConnector) Driver() driver.Driver { return mysqlDialectDriver{} }

type mysqlDialectDriver struct{}

func (mysqlDialectDriver) Open(string) (driver.Conn, error) { return nil, io.EOF }

type mysqlDialectConnection struct{ pingErr error }

func (mysqlDialectConnection) Prepare(string) (driver.Stmt, error) { return nil, io.EOF }
func (mysqlDialectConnection) Close() error                        { return nil }
func (mysqlDialectConnection) Begin() (driver.Tx, error)           { return nil, io.EOF }
func (c mysqlDialectConnection) Ping(context.Context) error        { return c.pingErr }

func TestDialectContract(t *testing.T) {
	dialect := Dialect{}
	if dialect.Name() != "mysql" || dialect.SQLDriver() != "mysql" {
		t.Fatalf("name=%q driver=%q", dialect.Name(), dialect.SQLDriver())
	}
	if _, err := dialect.DSN(config.Config{}); err == nil {
		t.Fatal("missing DSN accepted")
	}
	if dsn, err := dialect.DSN(config.Config{DatabaseDSN: " mysql://runtime "}); err != nil || dsn != "mysql://runtime" {
		t.Fatalf("dsn=%q err=%v", dsn, err)
	}
	if got := dialect.SQLDialect().Identifier("order_item"); got != "`order_item`" {
		t.Fatalf("identifier=%q", got)
	}
	assertMySQLIdentifierPanics(t, func() { dialect.SQLDialect().Identifier("order`item") })
	if got := dialect.SQLDialect().Placeholder(7); got != "?" {
		t.Fatalf("placeholder=%q", got)
	}
	if sql := dialect.SchemaMigrationSQL(); !strings.Contains(sql, "`_schema_migrations`") || !strings.Contains(sql, "PRIMARY KEY") {
		t.Fatalf("migration SQL=%q", sql)
	}
}

func assertMySQLIdentifierPanics(t *testing.T, run func()) {
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
		{name: "failure", pingErr: errors.New("connection refused"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(mysqlDialectConnector{pingErr: test.pingErr})
			t.Cleanup(func() { _ = db.Close() })
			err := dialect.Configure(t.Context(), db, "ignored")
			if (err != nil) != test.wantErr || test.wantErr && !strings.Contains(err.Error(), "connect mysql database: connection refused") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
