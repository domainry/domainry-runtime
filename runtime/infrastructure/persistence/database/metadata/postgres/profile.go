package postgres

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	metadatastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/storage"
)

type MetadataStorageProfile struct{}

func NewMetadataStorageProfile() MetadataStorageProfile { return MetadataStorageProfile{} }
func (MetadataStorageProfile) IDColumnType() string     { return "TEXT" }
func (MetadataStorageProfile) FieldColumnType(field definitionmodel.FieldSchema, _ bool) string {
	switch strings.TrimSpace(field.Type) {
	case "integer":
		return "BIGINT"
	case "currency", "percent":
		config := metadatastorage.DecimalConfig(field)
		return fmt.Sprintf("NUMERIC(%d,%d)", config.Precision, config.Scale)
	case "number":
		return "DOUBLE PRECISION"
	case "boolean":
		return "BOOLEAN"
	case "json":
		return "JSON"
	default:
		return "TEXT"
	}
}
func (MetadataStorageProfile) Columns(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, schema, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema = "+renderer.Placeholder(1)+" AND table_name = "+renderer.Placeholder(2), schema, table)
	return metadatastorage.ReadColumns(rows, err, table)
}
func (MetadataStorageProfile) ColumnTypes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, schema, table string) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_schema = "+renderer.Placeholder(1)+" AND table_name = "+renderer.Placeholder(2), schema, table)
	return metadatastorage.ReadColumnTypes(rows, err, table)
}
func (MetadataStorageProfile) Indexes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, schema, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT indexname FROM pg_indexes WHERE schemaname = "+renderer.Placeholder(1)+" AND tablename = "+renderer.Placeholder(2), schema, table)
	return metadatastorage.ReadIndexes(rows, err)
}
func (MetadataStorageProfile) DropIndex(ctx context.Context, executor metadatastorage.Executor, renderer ormbuilder.Renderer, _, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX IF EXISTS "+renderer.Identifier(index))
	return err
}
func (MetadataStorageProfile) ConditionalUniquePlan(renderer ormbuilder.Renderer, table, index string, policy recordvalidation.RecordConditionalUniquePolicy) metadatastorage.ConditionalUniquePlan {
	return metadatastorage.PartialConditionalUniquePlan(renderer, table, index, policy)
}
func (MetadataStorageProfile) ConditionalUniqueGuard(string) string { return "" }
func (MetadataStorageProfile) DropColumn(ctx context.Context, executor metadatastorage.Executor, renderer ormbuilder.Renderer, table, column string) error {
	_, err := executor.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" DROP COLUMN "+renderer.Identifier(column))
	return err
}
func (MetadataStorageProfile) ExactDecimalUpgradeAllowed(current string, field definitionmodel.FieldSchema) bool {
	if kind := strings.TrimSpace(field.Type); kind != "currency" && kind != "percent" {
		return false
	}
	normalized := metadatastorage.NormalizePhysicalType(current)
	return normalized == "REAL" || normalized == "DOUBLEPRECISION" || strings.HasPrefix(normalized, "NUMERIC(") || strings.HasPrefix(normalized, "DECIMAL(")
}
