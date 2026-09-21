package postgres

import (
	"context"
	"fmt"
	"strconv"
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
func (ApplicationSchemaStorageProfile) IDColumnType() string { return "TEXT" }
func (ApplicationSchemaStorageProfile) FieldColumnType(field definitionmodel.FieldSchema, _ bool) string {
	switch strings.TrimSpace(field.Type) {
	case "integer":
		return "BIGINT"
	case "currency", "percent":
		config := appschemastorage.DecimalConfig(field)
		return fmt.Sprintf("NUMERIC(%d,%d)", config.Precision, config.Scale)
	case "number":
		return "DOUBLE PRECISION"
	case "boolean":
		return "BOOLEAN"
	case "json":
		return "JSONB"
	case "file", "file_list":
		return "JSON"
	case "multi_select":
		return "TEXT"
	default:
		return "TEXT"
	}
}
func (ApplicationSchemaStorageProfile) Columns(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, schema, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema = "+renderer.Placeholder(1)+" AND table_name = "+renderer.Placeholder(2), schema, table)
	return appschemastorage.ReadColumns(rows, err, table)
}
func (ApplicationSchemaStorageProfile) ColumnTypes(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, schema, table string) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_schema = "+renderer.Placeholder(1)+" AND table_name = "+renderer.Placeholder(2), schema, table)
	return appschemastorage.ReadColumnTypes(rows, err, table)
}
func (ApplicationSchemaStorageProfile) Indexes(ctx context.Context, queryer appschemastorage.Queryer, renderer query.Renderer, schema, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT indexname FROM pg_indexes WHERE schemaname = "+renderer.Placeholder(1)+" AND tablename = "+renderer.Placeholder(2), schema, table)
	return appschemastorage.ReadIndexes(rows, err)
}
func (ApplicationSchemaStorageProfile) DropIndex(ctx context.Context, executor appschemastorage.Executor, renderer query.Renderer, _, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX IF EXISTS "+renderer.Identifier(index))
	return err
}
func (ApplicationSchemaStorageProfile) ConditionalUniquePlan(renderer query.Renderer, table, index string, policy recordvalidation.RecordConditionalUniquePolicy) appschemastorage.ConditionalUniquePlan {
	return appschemastorage.PartialConditionalUniquePlan(renderer, table, index, policy)
}
func (ApplicationSchemaStorageProfile) ConditionalUniqueGuard(string) string { return "" }
func (ApplicationSchemaStorageProfile) DropColumn(ctx context.Context, executor appschemastorage.Executor, renderer query.Renderer, table, column string) error {
	_, err := executor.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" DROP COLUMN "+renderer.Identifier(column))
	return err
}
func (ApplicationSchemaStorageProfile) ExactDecimalUpgradeAllowed(current string, field definitionmodel.FieldSchema) bool {
	if kind := strings.TrimSpace(field.Type); kind != "currency" && kind != "percent" {
		return false
	}
	normalized := appschemastorage.NormalizePhysicalType(current)
	return normalized == "REAL" || normalized == "DOUBLEPRECISION" || strings.HasPrefix(normalized, "NUMERIC(") || strings.HasPrefix(normalized, "DECIMAL(")
}

func (ApplicationSchemaStorageProfile) ExactDecimalPreflightSQL(table, column string, scale int) string {
	return "SELECT COUNT(*) FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + "::numeric <> ROUND(" + column + "::numeric, " + strconv.Itoa(scale) + ")"
}

func (ApplicationSchemaStorageProfile) ExactDecimalAlterSQL(table, column, target string) string {
	return "ALTER TABLE " + table + " ALTER COLUMN " + column + " TYPE " + target + " USING " + column + "::numeric"
}
