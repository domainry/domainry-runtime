package database

import (
	"github.com/domainry/domainry-foundation/mutation"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"database/sql"
	"fmt"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/failure"

	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"

	"strings"

	"github.com/domainry/domainry-foundation/secrets"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// The methods and functions in this file are the narrow SQL seams used by
// domain-owned repository adapter packages. They keep Store internals private
// while allowing adapters to leave the database root package.
func (s *RuntimeStore) TenantListWhereClause(workspaceID string, query recordmodel.RecordListQuery) (string, []any, error) {
	return querypersistence.BuildTenantWhere(s, workspaceID, query)
}

func (s *RuntimeStore) ListOrderClause(query recordmodel.RecordListQuery) string {
	return querypersistence.BuildOrder(s, query)
}

func MutationConstraintError(err error, resource, identifier string, kind mutation.MutationConflictKind) error {
	return failure.ConstraintError(err, resource, identifier, kind)
}

func MutationTransactionError(err error, resource, identifier string) error {
	return failure.TransactionError(err, resource, identifier)
}

func NullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func BoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *RuntimeStore) InsertStatement(table string, columns []string) string {
	return "INSERT INTO " + s.TableIdentifier(table) + " (" + strings.Join(QuotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
}

func (s *RuntimeStore) InsertSystemRowContext(ctx context.Context, table string, columns []string, values []any) error {
	return s.insertSystemRowContext(ctx, table, columns, values)
}

func QuotedColumns(store *RuntimeStore, columns []string) []string {
	return quotedColumns(store, columns)
}

func (s *RuntimeStore) SecretMaterialKey() [32]byte            { return s.secretMaterialKey }
func (s *RuntimeStore) SecretKeyProvider() secrets.KeyProvider { return s.secretKeyProvider }

func (s *RuntimeStore) CreateIndexIfMissing(ctx context.Context, table, index string, unique bool, columns ...string) error {
	return runtimeschema.CreateIndexIfMissing(ctx, s, table, index, unique, columns...)
}

func (s *RuntimeStore) EnsureRuntimeColumn(ctx context.Context, table, column, definition string) error {
	return s.ensureRuntimeColumn(ctx, table, column, definition)
}

func (s *RuntimeStore) MetadataIDColumnType() string { return s.metadataIDColumnType() }
func (s *RuntimeStore) RuntimeTableExists(ctx context.Context, table string) (bool, error) {
	base := s.sqlBase()
	query := base.RuntimeEngine.TableExistsQuery(base.SQLRenderer, base.DatabaseSchema, strings.TrimSpace(table))
	if strings.TrimSpace(query.Statement) == "" {
		return false, fmt.Errorf("database engine does not support table inspection")
	}
	var count int
	if err := s.schemaDatabase().QueryRowContext(ctx, query.Statement, query.Arguments...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
func (s *RuntimeStore) LocalizedTextKeyColumnType() string {
	return s.sqlBase().RuntimeEngine.TextKeyColumnType(128)
}
func (s *RuntimeStore) RuntimeColumnDefinition(definition string) string {
	return s.runtimeColumnDefinition(definition)
}

func ValidateExternalMigrationBackup(driverName, evidencePath string) error {
	_, err := validateExternalMigrationBackup(driverName, evidencePath)
	return err
}

func MigrationPathsForDialect(cfg config.Config, dialect driver.Dialect) ([]string, error) {
	return (&RuntimeStore{dialect: dialect}).migrationPaths(cfg)
}

func (s *RuntimeStore) CreateSQLiteMigrationBackup(ctx context.Context, cfg config.Config) (string, error) {
	return s.createSQLiteMigrationBackup(ctx, cfg)
}

func NonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func recordMutationTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}
