package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type Dialect struct{}

func (Dialect) Name() string { return "mysql" }

func (Dialect) TextKeyColumnType(maxLength int) string {
	return fmt.Sprintf("VARCHAR(%d)", maxLength)
}

func (Dialect) ApplyUpdateLock(builder *ormbuilder.SelectBuilder) *ormbuilder.SelectBuilder {
	return builder.ForUpdate()
}

func (Dialect) ApplyUpsert(builder *ormbuilder.InsertBuilder, _ []string, updateColumns ...string) *ormbuilder.InsertBuilder {
	assignments := make([]ormbuilder.Assignment, len(updateColumns))
	for index, column := range updateColumns {
		assignments[index] = ormbuilder.AssignExpression(column, ormbuilder.InsertedValue(column))
	}
	return builder.OnDuplicateKeyUpdate(assignments...)
}
func (Dialect) ApplyCreateIndex(builder *ormbuilder.CreateIndexBuilder) *ormbuilder.CreateIndexBuilder {
	return builder
}
func (Dialect) IsCreateIndexAlreadyExists(err error) bool {
	var mysqlError *mysqldriver.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1061
}

func (Dialect) SQLDriver() string { return "mysql" }

func (Dialect) DSN(cfg config.Config) (string, error) {
	if strings.TrimSpace(cfg.DatabaseDSN) == "" {
		return "", fmt.Errorf("DATABASE_DSN is required when DATABASE_DRIVER=mysql")
	}
	return strings.TrimSpace(cfg.DatabaseDSN), nil
}

func (Dialect) Configure(ctx context.Context, db *sql.DB, _ string) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect mysql database: %w", err)
	}
	return nil
}

func (Dialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.MySQL)
	return value
}

func (Dialect) SchemaMigrationSQL() string {
	return "CREATE TABLE IF NOT EXISTS `_schema_migrations` (`path` VARCHAR(255) PRIMARY KEY, `applied_at` VARCHAR(64) NOT NULL)"
}
