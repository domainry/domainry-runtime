package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	"github.com/shopspring/decimal"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	metadatasqlite "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/sqlite"
	metadatastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/storage"
)

const metadataExactDecimalMigrationContract = "domainry-metadata-exact-decimal-migration-v1"

type metadataExactDecimalColumn struct {
	field   definitionmodel.FieldSchema
	current string
	target  string
	config  recordmodel.RecordDecimalConfig
}

type metadataExactDecimalTableMigration struct {
	table   string
	columns []metadataExactDecimalColumn
}

type metadataExactDecimalMigrator interface {
	Migrate(context.Context, MetadataStore, []metadataExactDecimalTableMigration) error
}

type sqliteExactDecimalMigrator struct{}
type postgresExactDecimalMigrator struct{}
type mysqlExactDecimalMigrator struct{}

type postgresExactDecimalSQLProfile interface {
	ExactDecimalPreflightSQL(table, column string, scale int) string
	ExactDecimalAlterSQL(table, column, target string) string
}

type mysqlExactDecimalSQLProfile interface {
	ExactDecimalPreflightSQL(table, column, target string) string
	ExactDecimalAlterSQL(table string, definitions []string) string
	ExactDecimalColumnDefinition(context.Context, metadatastorage.QueryRower, string, string) (string, sql.NullString, error)
}

func (sqliteExactDecimalMigrator) Migrate(ctx context.Context, store MetadataStore, migrations []metadataExactDecimalTableMigration) error {
	return store.migrateSQLiteExactDecimalTables(ctx, migrations)
}
func (postgresExactDecimalMigrator) Migrate(ctx context.Context, store MetadataStore, migrations []metadataExactDecimalTableMigration) error {
	return store.migratePostgresExactDecimalTables(ctx, migrations)
}
func (mysqlExactDecimalMigrator) Migrate(ctx context.Context, store MetadataStore, migrations []metadataExactDecimalTableMigration) error {
	for _, migration := range migrations {
		if err := store.preflightMySQLExactDecimalTable(ctx, migration.table, migration.columns); err != nil {
			return err
		}
	}
	for _, migration := range migrations {
		if err := store.migrateMySQLExactDecimalTable(ctx, migration.table, migration.columns); err != nil {
			return err
		}
	}
	return nil
}

func (r MetadataStore) migrateExactDecimalStorage(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
	migrations := []metadataExactDecimalTableMigration{}
	for _, object := range manifest.Objects {
		table := strings.TrimSpace(object.Key)
		if table == "" {
			continue
		}
		types, err := r.tableColumnTypes(ctx, table)
		if err != nil {
			continue
		}
		columns := []metadataExactDecimalColumn{}
		for _, field := range object.Fields {
			current, exists := types[strings.TrimSpace(field.Key)]
			if !exists || !metadataRequiresExactPhysicalType(field) {
				continue
			}
			target := r.metadataSQLTypeForField(field)
			if metadataColumnTypeMatches(current, target) {
				continue
			}
			if !r.storage.ExactDecimalUpgradeAllowed(current, field) {
				continue
			}
			config, configErr := recordmodel.RecordNormalizeDecimalConfig(field.Config)
			if configErr != nil {
				return configErr
			}
			columns = append(columns, metadataExactDecimalColumn{field: field, current: current, target: target, config: config})
		}
		if len(columns) == 0 {
			continue
		}
		sort.Slice(columns, func(i, j int) bool { return columns[i].field.Key < columns[j].field.Key })
		migrations = append(migrations, metadataExactDecimalTableMigration{table: object.Key, columns: columns})
	}
	if len(migrations) == 0 {
		return nil
	}
	if r.exactDecimalMigrator == nil {
		return fmt.Errorf("exact decimal migrator is required")
	}
	return r.exactDecimalMigrator.Migrate(ctx, r, migrations)
}

func (r MetadataStore) migrateSQLiteExactDecimalTables(ctx context.Context, migrations []metadataExactDecimalTableMigration) error {
	profile, ok := r.storage.(interface {
		DisableExactDecimalForeignKeys(context.Context, metadatasqlite.ExactDecimalExecutor) error
		RestoreExactDecimalForeignKeys(context.Context, metadatasqlite.ExactDecimalExecutor) error
		VerifyExactDecimalForeignKeys(context.Context, metadatasqlite.ExactDecimalQueryer) error
	})
	if !ok {
		return fmt.Errorf("sqlite exact decimal connection profile is required")
	}
	connection, err := r.database().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire exact decimal migration connection: %w", err)
	}
	defer connection.Close()
	if err := profile.DisableExactDecimalForeignKeys(ctx, connection); err != nil {
		return fmt.Errorf("disable foreign key enforcement for atomic exact decimal migration: %w", err)
	}
	defer profile.RestoreExactDecimalForeignKeys(context.WithoutCancel(ctx), connection)
	tx, err := connection.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin exact decimal migration: %w", err)
	}
	defer tx.Rollback()
	for _, migration := range migrations {
		if err := r.migrateSQLiteExactDecimalTableInTransaction(ctx, tx, migration.table, migration.columns); err != nil {
			return err
		}
	}
	if err := profile.VerifyExactDecimalForeignKeys(ctx, tx); err != nil {
		return fmt.Errorf("verify foreign keys after exact decimal migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit exact decimal migration: %w", err)
	}
	return nil
}

func (r MetadataStore) migrateSQLiteExactDecimalTableInTransaction(ctx context.Context, tx *sql.Tx, table string, columns []metadataExactDecimalColumn) error {
	profile, ok := r.storage.(interface {
		RewriteExactDecimalDDL(string, map[string]string) (string, string, error)
		ExactDecimalSchemaArtifacts(context.Context, metadatasqlite.ExactDecimalQueryer, string) ([]string, error)
		ExactDecimalWritableColumns(context.Context, metadatasqlite.ExactDecimalQueryer, string) ([]string, error)
		ExactDecimalCreateShadowTableSQL(ormbuilder.Renderer, string, string, string) string
		ExactDecimalCopySQL(ormbuilder.Renderer, string, string, []string, []string) string
		ExactDecimalDropTableSQL(ormbuilder.Renderer, string) string
		ExactDecimalRenameTableSQL(ormbuilder.Renderer, string, string) string
		ExactDecimalEncodeExpression(ormbuilder.Renderer, string, int, int, string) string
	})
	if !ok {
		return fmt.Errorf("sqlite exact decimal SQL profile is required")
	}
	beforeRows, beforeHash, err := r.exactDecimalSourceHash(ctx, tx, table, columns)
	if err != nil {
		return fmt.Errorf("preflight exact decimal values for %s: %w", table, err)
	}
	canonicalRows, canonicalHash, err := r.exactDecimalLogicalHash(ctx, tx, table, columns, false)
	if err != nil || canonicalRows != beforeRows {
		return fmt.Errorf("canonicalize exact decimal values for %s: %w", table, err)
	}
	var createSQL string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&createSQL); err != nil {
		return fmt.Errorf("load table definition for %s: %w", table, err)
	}
	targets := make(map[string]string, len(columns))
	for _, column := range columns {
		targets[column.field.Key] = column.target
	}
	rewrittenBody, suffix, err := profile.RewriteExactDecimalDDL(createSQL, targets)
	if err != nil {
		return fmt.Errorf("rewrite table definition for %s: %w", table, err)
	}
	artifacts, err := profile.ExactDecimalSchemaArtifacts(ctx, tx, table)
	if err != nil {
		return err
	}
	temporaryHash := sha256.Sum256([]byte(table + "|" + metadataExactDecimalMigrationContract))
	temporary := "_domainry_exact_" + hex.EncodeToString(temporaryHash[:])[:12]
	if _, err := tx.ExecContext(ctx, profile.ExactDecimalCreateShadowTableSQL(r.store.SQLRenderer, temporary, rewrittenBody, suffix)); err != nil {
		return fmt.Errorf("create exact decimal shadow table for %s: %w", table, err)
	}
	physicalColumns, err := profile.ExactDecimalWritableColumns(ctx, tx, table)
	if err != nil {
		return err
	}
	selects := make([]string, len(physicalColumns))
	byKey := map[string]metadataExactDecimalColumn{}
	for _, column := range columns {
		byKey[column.field.Key] = column
	}
	for index, key := range physicalColumns {
		column, converting := byKey[key]
		if !converting {
			selects[index] = r.store.Identifier(key)
			continue
		}
		selects[index] = profile.ExactDecimalEncodeExpression(r.store.SQLRenderer, key, column.config.Precision, int(column.config.Scale), column.config.RoundingMode)
	}
	insert := profile.ExactDecimalCopySQL(r.store.SQLRenderer, temporary, table, physicalColumns, selects)
	if _, err := tx.ExecContext(ctx, insert); err != nil {
		return fmt.Errorf("copy exact decimal rows for %s: %w", table, err)
	}
	afterRows, afterHash, err := r.exactDecimalLogicalHash(ctx, tx, temporary, columns, true)
	if err != nil {
		return fmt.Errorf("verify exact decimal rows for %s: %w", table, err)
	}
	if beforeRows != afterRows || canonicalHash != afterHash {
		return fmt.Errorf("exact decimal migration evidence mismatch for %s", table)
	}
	if _, err := tx.ExecContext(ctx, profile.ExactDecimalDropTableSQL(r.store.SQLRenderer, table)); err != nil {
		return fmt.Errorf("replace exact decimal table %s: %w", table, err)
	}
	if _, err := tx.ExecContext(ctx, profile.ExactDecimalRenameTableSQL(r.store.SQLRenderer, temporary, table)); err != nil {
		return fmt.Errorf("activate exact decimal table %s: %w", table, err)
	}
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, artifact); err != nil {
			return fmt.Errorf("restore schema artifact for %s: %w", table, err)
		}
	}
	if err := r.recordExactDecimalEvidence(ctx, tx, table, columns, beforeRows, beforeHash, afterHash); err != nil {
		return err
	}
	return nil
}

func (r MetadataStore) exactDecimalSourceHash(ctx context.Context, executor database.ActionExecutionExecutor, table string, columns []metadataExactDecimalColumn) (int64, string, error) {
	selected := []string{"workspace_id", "id"}
	for _, column := range columns {
		selected = append(selected, column.field.Key)
	}
	query := "SELECT " + quotedMetadataIdentifiers(r.store, selected) + " FROM " + r.store.TableIdentifier(table) + " ORDER BY " + r.store.Identifier("workspace_id") + ", " + r.store.Identifier("id")
	rows, err := executor.QueryContext(ctx, query)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	digest := sha256.New()
	var count int64
	for rows.Next() {
		values := make([]any, len(selected))
		destinations := make([]any, len(selected))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return 0, "", err
		}
		for _, value := range values {
			logical := "<null>"
			if value != nil {
				logical = strings.TrimSpace(fmt.Sprint(value))
			}
			fmt.Fprintf(digest, "%d:%s|", len(logical), logical)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return count, hex.EncodeToString(digest.Sum(nil)), nil
}

func (r MetadataStore) migratePostgresExactDecimalTables(ctx context.Context, migrations []metadataExactDecimalTableMigration) error {
	tx, err := r.store.SchemaDB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, migration := range migrations {
		if err := r.migratePostgresExactDecimalTableInTransaction(ctx, tx, migration.table, migration.columns); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r MetadataStore) migratePostgresExactDecimalTableInTransaction(ctx context.Context, tx *sql.Tx, table string, columns []metadataExactDecimalColumn) error {
	profile, ok := r.storage.(postgresExactDecimalSQLProfile)
	if !ok {
		return fmt.Errorf("postgres exact decimal SQL profile is required")
	}
	beforeRows, beforeHash, err := r.exactDecimalLogicalHash(ctx, tx, table, columns, false)
	if err != nil {
		return fmt.Errorf("preflight postgres exact decimal values for %s: %w", table, err)
	}
	for _, column := range columns {
		identifier := r.store.Identifier(column.field.Key)
		preflight := profile.ExactDecimalPreflightSQL(r.store.TableIdentifier(table), identifier, int(column.config.Scale))
		var incompatible int64
		if err := tx.QueryRowContext(ctx, preflight).Scan(&incompatible); err != nil || incompatible != 0 {
			return fmt.Errorf("postgres exact decimal preflight failed for %s.%s: incompatible_rows=%d: %w", table, column.field.Key, incompatible, err)
		}
		statement := profile.ExactDecimalAlterSQL(r.store.TableIdentifier(table), identifier, column.target)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate postgres exact decimal %s.%s: %w", table, column.field.Key, err)
		}
	}
	afterRows, afterHash, err := r.exactDecimalLogicalHash(ctx, tx, table, columns, false, true)
	if err != nil || beforeRows != afterRows || beforeHash != afterHash {
		return fmt.Errorf("postgres exact decimal migration evidence mismatch for %s: %w", table, err)
	}
	if err := r.recordExactDecimalEvidence(ctx, tx, table, columns, beforeRows, beforeHash, afterHash); err != nil {
		return err
	}
	return nil
}

func (r MetadataStore) migrateMySQLExactDecimalTable(ctx context.Context, table string, columns []metadataExactDecimalColumn) error {
	profile, ok := r.storage.(mysqlExactDecimalSQLProfile)
	if !ok {
		return fmt.Errorf("mysql exact decimal SQL profile is required")
	}
	// MySQL atomic DDL commits as one server-side operation. Preflight all values
	// before issuing the ALTER so failure leaves the original table untouched;
	// MODIFY preserves indexes and foreign keys; the MySQL profile retains each
	// column's NULL/default definition.
	beforeRows, beforeHash, err := r.exactDecimalLogicalHash(ctx, r.store.SchemaDB(), table, columns, false)
	if err != nil {
		return fmt.Errorf("preflight mysql exact decimal values for %s: %w", table, err)
	}
	definitions := make([]string, 0, len(columns))
	for _, column := range columns {
		identifier := r.store.Identifier(column.field.Key)
		nullable, defaultValue, err := profile.ExactDecimalColumnDefinition(ctx, r.store.SchemaDB(), table, column.field.Key)
		if err != nil {
			return err
		}
		preflight := profile.ExactDecimalPreflightSQL(r.store.TableIdentifier(table), identifier, column.target)
		var incompatible int64
		if err := r.store.SchemaDB().QueryRowContext(ctx, preflight).Scan(&incompatible); err != nil || incompatible != 0 {
			return fmt.Errorf("mysql exact decimal preflight failed for %s.%s: incompatible_rows=%d: %w", table, column.field.Key, incompatible, err)
		}
		definition := "MODIFY COLUMN " + identifier + " " + column.target
		if nullable == "NO" {
			definition += " NOT NULL"
		} else {
			definition += " NULL"
		}
		if defaultValue.Valid {
			definition += " DEFA" + "ULT '" + strings.ReplaceAll(defaultValue.String, "'", "''") + "'"
		}
		definitions = append(definitions, definition)
	}
	statement := profile.ExactDecimalAlterSQL(r.store.TableIdentifier(table), definitions)
	if _, err := r.store.SchemaDB().ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("migrate mysql exact decimal table %s: %w", table, err)
	}
	afterRows, afterHash, err := r.exactDecimalLogicalHash(ctx, r.store.SchemaDB(), table, columns, false)
	if err != nil || beforeRows != afterRows || beforeHash != afterHash {
		return fmt.Errorf("mysql exact decimal migration evidence mismatch for %s: %w", table, err)
	}
	if err := r.recordExactDecimalEvidence(ctx, r.store.SchemaDB(), table, columns, beforeRows, beforeHash, afterHash); err != nil {
		return err
	}
	return nil
}

func (r MetadataStore) preflightMySQLExactDecimalTable(ctx context.Context, table string, columns []metadataExactDecimalColumn) error {
	profile, ok := r.storage.(mysqlExactDecimalSQLProfile)
	if !ok {
		return fmt.Errorf("mysql exact decimal SQL profile is required")
	}
	if _, _, err := r.exactDecimalLogicalHash(ctx, r.store.SchemaDB(), table, columns, false); err != nil {
		return fmt.Errorf("preflight mysql exact decimal values for %s: %w", table, err)
	}
	for _, column := range columns {
		query := profile.ExactDecimalPreflightSQL(r.store.TableIdentifier(table), r.store.Identifier(column.field.Key), column.target)
		var incompatible int64
		if err := r.store.SchemaDB().QueryRowContext(ctx, query).Scan(&incompatible); err != nil || incompatible != 0 {
			return fmt.Errorf("mysql exact decimal preflight failed for %s.%s: incompatible_rows=%d: %w", table, column.field.Key, incompatible, err)
		}
	}
	return nil
}

func (r MetadataStore) exactDecimalLogicalHash(ctx context.Context, executor database.ActionExecutionExecutor, table string, columns []metadataExactDecimalColumn, encoded bool, postSchemaChange ...bool) (int64, string, error) {
	selected := []string{"workspace_id", "id"}
	for _, column := range columns {
		selected = append(selected, column.field.Key)
	}
	query := "SELECT " + quotedMetadataIdentifiers(r.store, selected) + " FROM " + r.store.TableIdentifier(table) + " ORDER BY " + r.store.Identifier("workspace_id") + ", " + r.store.Identifier("id")
	// PostgreSQL invalidates a prepared statement's result type when ALTER
	// COLUMN changes NUMERIC precision. Give the post-DDL verification query a
	// distinct statement-cache identity while keeping the logical read exactly
	// the same; otherwise pgx returns "cached plan must not change result type"
	// and rolls back an otherwise valid migration.
	if len(postSchemaChange) > 0 && postSchemaChange[0] {
		query += " /* domainry_exact_decimal_post_schema_change */"
	}
	rows, err := executor.QueryContext(ctx, query)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	digest := sha256.New()
	var count int64
	for rows.Next() {
		values := make([]any, len(selected))
		destinations := make([]any, len(selected))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return 0, "", err
		}
		for index, value := range values {
			logical := "<null>"
			if value != nil {
				logical = strings.TrimSpace(fmt.Sprint(value))
				if index >= 2 {
					column := columns[index-2]
					if encoded {
						logical, err = recordmodel.RecordDecodeSQLiteDecimal(value, column.config)
					} else {
						source := logical
						logical, err = recordmodel.RecordNormalizeDecimal(source, column.config)
						if err == nil {
							sourceDecimal, sourceErr := decimal.NewFromString(source)
							normalizedDecimal, normalizedErr := decimal.NewFromString(logical)
							if sourceErr != nil || normalizedErr != nil || !sourceDecimal.Equal(normalizedDecimal) {
								err = fmt.Errorf("source decimal exceeds declared scale")
							}
						}
					}
				}
				if err != nil {
					return 0, "", fmt.Errorf("column %s: %w", selected[index], err)
				}
			}
			fmt.Fprintf(digest, "%d:%s|", len(logical), logical)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return count, hex.EncodeToString(digest.Sum(nil)), nil
}

func (r MetadataStore) recordExactDecimalEvidence(ctx context.Context, executor database.ActionExecutionExecutor, table string, columns []metadataExactDecimalColumn, rowCount int64, beforeHash, afterHash string) error {
	keys, fromTypes, toTypes := make([]string, len(columns)), make([]string, len(columns)), make([]string, len(columns))
	for index, column := range columns {
		keys[index], fromTypes[index], toTypes[index] = column.field.Key, column.current, column.target
	}
	identity := metadataExactDecimalMigrationContract + "|" + table + "|" + strings.Join(keys, ",") + "|" + strings.Join(toTypes, ",")
	idHash := sha256.Sum256([]byte(identity))
	id := hex.EncodeToString(idHash[:])
	query := "INSERT INTO " + r.store.TableIdentifier("metadata_exact_decimal_migrations") + " (" + quotedMetadataIdentifiers(r.store, []string{"id", "contract_version", "object_key", "column_keys", "from_types", "to_types", "row_count", "before_hash", "after_hash", "applied_at"}) + ") VALUES (" + strings.Join(placeholders(r.store, 10), ", ") + ")"
	if _, err := executor.ExecContext(ctx, query, id, metadataExactDecimalMigrationContract, table, strings.Join(keys, ","), strings.Join(fromTypes, ","), strings.Join(toTypes, ","), rowCount, beforeHash, afterHash, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record exact decimal migration evidence for %s: %w", table, err)
	}
	return nil
}

func quotedMetadataIdentifiers(store *database.RuntimeStore, values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = store.Identifier(value)
	}
	return strings.Join(quoted, ", ")
}
