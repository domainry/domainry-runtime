package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
	connection, err := r.database().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire exact decimal migration connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign key enforcement for atomic exact decimal migration: %w", err)
	}
	defer connection.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
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
	foreignKeyRows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("verify foreign keys after exact decimal migration: %w", err)
	}
	if foreignKeyRows.Next() {
		foreignKeyRows.Close()
		return fmt.Errorf("foreign key violation after exact decimal migration")
	}
	if err := foreignKeyRows.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit exact decimal migration: %w", err)
	}
	return nil
}

func (r MetadataStore) migrateSQLiteExactDecimalTableInTransaction(ctx context.Context, tx *sql.Tx, table string, columns []metadataExactDecimalColumn) error {
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
	rewrittenBody, suffix, err := rewriteSQLiteExactDecimalDDL(createSQL, columns)
	if err != nil {
		return fmt.Errorf("rewrite table definition for %s: %w", table, err)
	}
	artifacts, err := sqliteSchemaArtifacts(ctx, tx, table)
	if err != nil {
		return err
	}
	temporaryHash := sha256.Sum256([]byte(table + "|" + metadataExactDecimalMigrationContract))
	temporary := "_domainry_exact_" + hex.EncodeToString(temporaryHash[:])[:12]
	if _, err := tx.ExecContext(ctx, "CREATE TABLE "+r.store.TableIdentifier(temporary)+" ("+rewrittenBody+")"+suffix); err != nil {
		return fmt.Errorf("create exact decimal shadow table for %s: %w", table, err)
	}
	physicalColumns, err := sqliteWritableColumns(ctx, tx, table)
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
		selects[index] = "runtime_exact_decimal_encode(" + r.store.Identifier(key) + ", " + strconv.Itoa(column.config.Precision) + ", " + strconv.Itoa(int(column.config.Scale)) + ", '" + strings.ReplaceAll(column.config.RoundingMode, "'", "''") + "')"
	}
	insert := "INSERT INTO " + r.store.TableIdentifier(temporary) + " (" + quotedMetadataIdentifiers(r.store, physicalColumns) + ") SELECT " + strings.Join(selects, ", ") + " FROM " + r.store.TableIdentifier(table)
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
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+r.store.TableIdentifier(table)); err != nil {
		return fmt.Errorf("replace exact decimal table %s: %w", table, err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE "+r.store.TableIdentifier(temporary)+" RENAME TO "+r.store.Identifier(table)); err != nil {
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
	beforeRows, beforeHash, err := r.exactDecimalLogicalHash(ctx, tx, table, columns, false)
	if err != nil {
		return fmt.Errorf("preflight postgres exact decimal values for %s: %w", table, err)
	}
	for _, column := range columns {
		identifier := r.store.Identifier(column.field.Key)
		preflight := postgresExactDecimalPreflightSQL(r.store.TableIdentifier(table), identifier, int(column.config.Scale))
		var incompatible int64
		if err := tx.QueryRowContext(ctx, preflight).Scan(&incompatible); err != nil || incompatible != 0 {
			return fmt.Errorf("postgres exact decimal preflight failed for %s.%s: incompatible_rows=%d: %w", table, column.field.Key, incompatible, err)
		}
		statement := postgresExactDecimalAlterSQL(r.store.TableIdentifier(table), identifier, column.target)
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
	// MySQL atomic DDL commits as one server-side operation. Preflight all values
	// before issuing the ALTER so failure leaves the original table untouched;
	// MODIFY preserves indexes and foreign keys and we retain NULL/default from
	// information_schema in the generated definition.
	beforeRows, beforeHash, err := r.exactDecimalLogicalHash(ctx, r.store.SchemaDB(), table, columns, false)
	if err != nil {
		return fmt.Errorf("preflight mysql exact decimal values for %s: %w", table, err)
	}
	definitions := make([]string, 0, len(columns))
	for _, column := range columns {
		identifier := r.store.Identifier(column.field.Key)
		var nullable string
		var defaultValue sql.NullString
		query := "SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?"
		if err := r.store.SchemaDB().QueryRowContext(ctx, query, table, column.field.Key).Scan(&nullable, &defaultValue); err != nil {
			return err
		}
		preflight := mysqlExactDecimalPreflightSQL(r.store.TableIdentifier(table), identifier, column.target)
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
	statement := mysqlExactDecimalAlterSQL(r.store.TableIdentifier(table), definitions)
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
	if _, _, err := r.exactDecimalLogicalHash(ctx, r.store.SchemaDB(), table, columns, false); err != nil {
		return fmt.Errorf("preflight mysql exact decimal values for %s: %w", table, err)
	}
	for _, column := range columns {
		query := mysqlExactDecimalPreflightSQL(r.store.TableIdentifier(table), r.store.Identifier(column.field.Key), column.target)
		var incompatible int64
		if err := r.store.SchemaDB().QueryRowContext(ctx, query).Scan(&incompatible); err != nil || incompatible != 0 {
			return fmt.Errorf("mysql exact decimal preflight failed for %s.%s: incompatible_rows=%d: %w", table, column.field.Key, incompatible, err)
		}
	}
	return nil
}

func postgresExactDecimalPreflightSQL(table, column string, scale int) string {
	return "SELECT COUNT(*) FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + "::numeric <> ROUND(" + column + "::numeric, " + strconv.Itoa(scale) + ")"
}

func postgresExactDecimalAlterSQL(table, column, target string) string {
	return "ALTER TABLE " + table + " ALTER COLUMN " + column + " TYPE " + target + " USING " + column + "::numeric"
}

func mysqlExactDecimalPreflightSQL(table, column, target string) string {
	return "SELECT COUNT(*) FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + " <> CAST(" + column + " AS " + target + ")"
}

func mysqlExactDecimalAlterSQL(table string, definitions []string) string {
	return "ALTER TABLE " + table + " " + strings.Join(definitions, ", ") + ", ALGORITHM=COPY"
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

func sqliteWritableColumns(ctx context.Context, executor database.ActionExecutionExecutor, table string) ([]string, error) {
	rows, err := executor.QueryContext(ctx, "PRAGMA table_xinfo(\""+strings.ReplaceAll(table, "\"", "\"\"")+"\")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return nil, err
		}
		if hidden == 0 {
			columns = append(columns, name)
		}
	}
	return columns, rows.Err()
}

func sqliteSchemaArtifacts(ctx context.Context, executor database.ActionExecutionExecutor, table string) ([]string, error) {
	rows, err := executor.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type, name", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	artifacts := []string{}
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, statement)
	}
	return artifacts, rows.Err()
}

func rewriteSQLiteExactDecimalDDL(createSQL string, columns []metadataExactDecimalColumn) (string, string, error) {
	open, close := sqliteDDLBodyBounds(createSQL)
	if open < 0 || close <= open {
		return "", "", fmt.Errorf("unsupported CREATE TABLE definition")
	}
	parts := sqliteSplitTopLevel(createSQL[open+1 : close])
	targets := map[string]string{}
	for _, column := range columns {
		targets[column.field.Key] = column.target
	}
	seen := map[string]bool{}
	for index, part := range parts {
		key, nameEnd := sqliteLeadingIdentifier(part)
		target, exists := targets[key]
		if !exists {
			continue
		}
		typeEnd := sqliteColumnTypeEnd(part, nameEnd)
		remainder := strings.TrimLeftFunc(part[typeEnd:], unicode.IsSpace)
		parts[index] = part[:nameEnd] + " " + target
		if remainder != "" {
			parts[index] += " " + remainder
		}
		seen[key] = true
	}
	for key := range targets {
		if !seen[key] {
			return "", "", fmt.Errorf("column definition not found: %s", key)
		}
	}
	return strings.Join(parts, ","), createSQL[close+1:], nil
}

func sqliteDDLBodyBounds(value string) (int, int) {
	open := strings.Index(value, "(")
	if open < 0 {
		return -1, -1
	}
	depth, quote := 0, rune(0)
	for index, char := range value[open:] {
		absolute := open + index
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' || char == ']' {
			quote = char
			if char == ']' {
				quote = ']'
			}
			continue
		}
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return open, absolute
			}
		}
	}
	return open, -1
}

func sqliteSplitTopLevel(value string) []string {
	parts, start, depth, quote := []string{}, 0, 0, rune(0)
	for index, char := range value {
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' || char == ']' {
			quote = char
			continue
		}
		if char == '(' {
			depth++
		} else if char == ')' {
			depth--
		} else if char == ',' && depth == 0 {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	return append(parts, value[start:])
}

func sqliteLeadingIdentifier(value string) (string, int) {
	start := 0
	for start < len(value) && unicode.IsSpace(rune(value[start])) {
		start++
	}
	if start >= len(value) {
		return "", start
	}
	if strings.ContainsRune("\"`[", rune(value[start])) {
		closing := byte(value[start])
		if closing == '[' {
			closing = ']'
		}
		end := strings.IndexByte(value[start+1:], closing)
		if end < 0 {
			return "", start
		}
		end += start + 1
		return value[start+1 : end], end + 1
	}
	end := start
	for end < len(value) && !unicode.IsSpace(rune(value[end])) {
		end++
	}
	return strings.Trim(value[start:end], "\"`[]"), end
}

func sqliteColumnTypeEnd(value string, start int) int {
	keywords := map[string]bool{"PRIMARY": true, "NOT": true, "UNIQUE": true, "CHECK": true, "DEFA" + "ULT": true, "COLLATE": true, "REFERENCES": true, "GENERATED": true, "AS": true}
	index := start
	for index < len(value) {
		for index < len(value) && unicode.IsSpace(rune(value[index])) {
			index++
		}
		wordStart := index
		for index < len(value) && (unicode.IsLetter(rune(value[index])) || value[index] == '_') {
			index++
		}
		if wordStart == index {
			index++
			continue
		}
		if keywords[strings.ToUpper(value[wordStart:index])] {
			return wordStart
		}
	}
	return len(value)
}
