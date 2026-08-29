package sqlite

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
	case "integer", "boolean":
		return "INTEGER"
	case "number":
		return "REAL"
	default:
		return "TEXT"
	}
}
func (MetadataStorageProfile) Columns(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
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
func (MetadataStorageProfile) ColumnTypes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]string, error) {
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
func (MetadataStorageProfile) Indexes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name="+renderer.Placeholder(1), table)
	return metadatastorage.ReadIndexes(rows, err)
}
func (MetadataStorageProfile) DropIndex(ctx context.Context, executor metadatastorage.Executor, renderer ormbuilder.Renderer, _, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX IF EXISTS "+renderer.Identifier(index))
	return err
}
