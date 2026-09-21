package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
)

type ApplicationSchemaStorageProfile struct{}

func NewApplicationSchemaStorageProfile() ApplicationSchemaStorageProfile {
	return ApplicationSchemaStorageProfile{}
}
func (ApplicationSchemaStorageProfile) IDColumnType() string { return "VARCHAR(191)" }
func (ApplicationSchemaStorageProfile) FieldColumnType(field definitionmodel.FieldSchema, indexed bool) string {
	switch strings.TrimSpace(field.Type) {
	case "integer":
		return "BIGINT"
	case "currency", "percent":
		config := appschemastorage.DecimalConfig(field)
		return fmt.Sprintf("DECIMAL(%d,%d)", config.Precision, config.Scale)
	case "number":
		return "DOUBLE"
	case "boolean":
		return "BOOLEAN"
	case "json", "file", "file_list":
		return "JSON"
	case "multi_select":
		return "TEXT"
	default:
		if strings.TrimSpace(field.Type) != "long_text" && indexed {
			return fmt.Sprintf("VARCHAR(%d)", appschemastorage.IndexedTextLength(field))
		}
		return "TEXT"
	}
}
func (ApplicationSchemaStorageProfile) Columns(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_name = "+renderer.Placeholder(1)+" AND table_schema = DATABASE()", table)
	return appschemastorage.ReadColumns(rows, err, table)
}
func (ApplicationSchemaStorageProfile) ColumnTypes(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, _ string, table string) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_name = "+renderer.Placeholder(1)+" AND table_schema = DATABASE()", table)
	return appschemastorage.ReadColumnTypes(rows, err, table)
}
func (ApplicationSchemaStorageProfile) Indexes(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = "+renderer.Placeholder(1), table)
	return appschemastorage.ReadIndexes(rows, err)
}

func (ApplicationSchemaStorageProfile) PhysicalSchema(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, _ string, tables []string) (appschemastorage.PhysicalSchemaSnapshot, error) {
	snapshot := appschemastorage.PhysicalSchemaSnapshot{
		ColumnsByTable: map[string]map[string]string{},
		IndexesByTable: map[string]map[string]bool{},
	}
	if len(tables) == 0 {
		return snapshot, nil
	}
	placeholders := make([]string, len(tables))
	args := make([]any, len(tables))
	for index, table := range tables {
		placeholders[index] = renderer.Placeholder(index + 1)
		args[index] = table
		snapshot.IndexesByTable[table] = map[string]bool{}
	}
	tableFilter := "(" + strings.Join(placeholders, ", ") + ")"
	rows, err := queryer.QueryContext(ctx, "SELECT table_name, column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name IN "+tableFilter, args...)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var table, name, dataType string
		var precision, scale sql.NullInt64
		if err := rows.Scan(&table, &name, &dataType, &precision, &scale); err != nil {
			_ = rows.Close()
			return snapshot, err
		}
		if precision.Valid && scale.Valid && (strings.EqualFold(dataType, "decimal") || strings.EqualFold(dataType, "numeric")) {
			dataType = fmt.Sprintf("%s(%d,%d)", dataType, precision.Int64, scale.Int64)
		}
		if snapshot.ColumnsByTable[table] == nil {
			snapshot.ColumnsByTable[table] = map[string]string{}
		}
		snapshot.ColumnsByTable[table][name] = dataType
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return snapshot, err
	}
	if err := rows.Close(); err != nil {
		return snapshot, err
	}
	rows, err = queryer.QueryContext(ctx, "SELECT DISTINCT table_name, index_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name IN "+tableFilter, args...)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			return snapshot, err
		}
		if snapshot.IndexesByTable[table] == nil {
			snapshot.IndexesByTable[table] = map[string]bool{}
		}
		snapshot.IndexesByTable[table][name] = true
	}
	return snapshot, rows.Err()
}
func (ApplicationSchemaStorageProfile) DropIndex(ctx context.Context, executor appschemastorage.Executor, renderer query.Renderer, table, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX "+renderer.Identifier(index)+" ON "+renderer.Table(table))
	return err
}
func (ApplicationSchemaStorageProfile) ConditionalUniquePlan(renderer query.Renderer, table, index string, policy recordvalidation.RecordConditionalUniquePolicy) appschemastorage.ConditionalUniquePlan {
	guard := appschemastorage.GuardColumn(index)
	fields := append(appschemastorage.ConditionalUniqueFields(policy), guard)
	statement := "ALTER TABLE " + renderer.Table(table) + " ADD COLUMN " + renderer.Identifier(guard) + " TINYINT GENERATED ALWAYS AS (CASE WHEN " + appschemastorage.ConditionalUniqueCondition(renderer, policy) + " THEN 1 ELSE NULL END) STORED"
	return appschemastorage.ConditionalUniquePlan{GuardColumn: guard, AddGuardStatement: statement, IndexFields: fields}
}
func (ApplicationSchemaStorageProfile) ConditionalUniqueGuard(index string) string {
	return appschemastorage.GuardColumn(index)
}
func (ApplicationSchemaStorageProfile) DropColumn(ctx context.Context, executor appschemastorage.Executor, renderer query.Renderer, table, column string) error {
	_, err := executor.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" DROP COLUMN "+renderer.Identifier(column))
	return err
}
func (ApplicationSchemaStorageProfile) ExactDecimalUpgradeAllowed(current string, field definitionmodel.FieldSchema) bool {
	if kind := strings.TrimSpace(field.Type); kind != "currency" && kind != "percent" {
		return false
	}
	normalized := appschemastorage.NormalizePhysicalType(current)
	return strings.HasPrefix(normalized, "FLOAT") || strings.HasPrefix(normalized, "DOUBLE") || strings.HasPrefix(normalized, "DECIMAL(") || strings.HasPrefix(normalized, "NUMERIC(")
}

func (ApplicationSchemaStorageProfile) ExactDecimalPreflightSQL(table, column, target string) string {
	return "SELECT COUNT(*) FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + " <> CAST(" + column + " AS " + target + ")"
}

func (ApplicationSchemaStorageProfile) ExactDecimalAlterSQL(table string, definitions []string) string {
	return "ALTER TABLE " + table + " " + strings.Join(definitions, ", ") + ", ALGORITHM=COPY"
}

func (ApplicationSchemaStorageProfile) ExactDecimalColumnDefinition(ctx context.Context, queryer appschemastorage.QueryRower, table, column string) (string, sql.NullString, error) {
	var nullable string
	var defaultValue sql.NullString
	err := queryer.QueryRowContext(ctx, "SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&nullable, &defaultValue)
	return nullable, defaultValue, err
}
