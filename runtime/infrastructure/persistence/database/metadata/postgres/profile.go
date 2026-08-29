package postgres

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
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
