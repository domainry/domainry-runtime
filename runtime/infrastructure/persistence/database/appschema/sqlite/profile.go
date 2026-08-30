package sqlite

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
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
	case "integer", "boolean":
		return "INTEGER"
	case "number":
		return "REAL"
	default:
		return "TEXT"
	}
}
func (ApplicationSchemaStorageProfile) Columns(ctx context.Context, queryer appschemastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_info("+renderer.Identifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		result[name] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return result, rows.Err()
}
func (ApplicationSchemaStorageProfile) ColumnTypes(ctx context.Context, queryer appschemastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, "PRAGMA table_info("+renderer.Identifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		result[name] = dataType
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return result, rows.Err()
}
func (ApplicationSchemaStorageProfile) Indexes(ctx context.Context, queryer appschemastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name="+renderer.Placeholder(1), table)
	return appschemastorage.ReadIndexes(rows, err)
}
func (ApplicationSchemaStorageProfile) DropIndex(ctx context.Context, executor appschemastorage.Executor, renderer ormbuilder.Renderer, _, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX IF EXISTS "+renderer.Identifier(index))
	return err
}
func (ApplicationSchemaStorageProfile) ConditionalUniquePlan(renderer ormbuilder.Renderer, table, index string, policy recordvalidation.RecordConditionalUniquePolicy) appschemastorage.ConditionalUniquePlan {
	return appschemastorage.PartialConditionalUniquePlan(renderer, table, index, policy)
}
func (ApplicationSchemaStorageProfile) ConditionalUniqueGuard(string) string { return "" }
func (ApplicationSchemaStorageProfile) DropColumn(ctx context.Context, executor appschemastorage.Executor, renderer ormbuilder.Renderer, table, column string) error {
	_, err := executor.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" DROP COLUMN "+renderer.Identifier(column))
	return err
}
func (ApplicationSchemaStorageProfile) ExactDecimalUpgradeAllowed(current string, field definitionmodel.FieldSchema) bool {
	if kind := strings.TrimSpace(field.Type); kind != "currency" && kind != "percent" {
		return false
	}
	switch appschemastorage.NormalizePhysicalType(current) {
	case "REAL", "DOUBLE", "FLOAT", "NUMERIC":
		return true
	default:
		return false
	}
}
