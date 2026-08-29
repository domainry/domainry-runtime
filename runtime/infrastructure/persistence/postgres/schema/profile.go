package schema

import (
	"context"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool        { return true }
func (Profile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (Profile) ApplicationTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1), Arguments: []any{databaseSchema}}
}
func (Profile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, databaseSchema, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1) + " AND table_name = " + renderer.Placeholder(2), Arguments: []any{databaseSchema, table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, databaseSchema, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT indexname FROM pg_indexes WHERE schemaname = " + renderer.Placeholder(1) + " AND tablename = " + renderer.Placeholder(2), Arguments: []any{databaseSchema, table}}
}

func (Profile) InspectModuleSchemaTable(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer, databaseSchema, table string) (persistencedriver.ModuleSchemaTable, bool, error) {
	schema := strings.TrimSpace(databaseSchema)
	if schema == "" {
		schema = "public"
	}
	rows, err := database.QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = `+renderer.Placeholder(1)+` AND table_name = `+renderer.Placeholder(2)+` ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	result := persistencedriver.ModuleSchemaTable{}
	for rows.Next() {
		var column persistencedriver.ModuleSchemaColumn
		var nullable string
		if err := rows.Scan(&column.Name, &column.Physical, &nullable); err != nil {
			_ = rows.Close()
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		column.Nullable = nullable == "YES"
		result.Columns = append(result.Columns, column)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	_ = rows.Close()
	if len(result.Columns) == 0 {
		return persistencedriver.ModuleSchemaTable{}, false, nil
	}
	primaryRows, err := database.QueryContext(ctx, `SELECT attribute.attname FROM pg_index idx JOIN pg_class relation ON relation.oid = idx.indrelid JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace JOIN LATERAL unnest(idx.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON true JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = key_column.attnum WHERE namespace.nspname = `+renderer.Placeholder(1)+` AND relation.relname = `+renderer.Placeholder(2)+` AND idx.indisprimary ORDER BY key_column.ordinality`, schema, table)
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	primary := map[string]bool{}
	for primaryRows.Next() {
		var name string
		if err := primaryRows.Scan(&name); err != nil {
			_ = primaryRows.Close()
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		primary[name] = true
	}
	if err := primaryRows.Err(); err != nil {
		_ = primaryRows.Close()
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	_ = primaryRows.Close()
	for index := range result.Columns {
		result.Columns[index].PrimaryKey = primary[result.Columns[index].Name]
	}
	indexRows, err := database.QueryContext(ctx, `SELECT index_class.relname, idx.indisunique, array_to_string(array_agg(attribute.attname ORDER BY key_column.ordinality), ',') FROM pg_index idx JOIN pg_class relation ON relation.oid = idx.indrelid JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace JOIN pg_class index_class ON index_class.oid = idx.indexrelid JOIN LATERAL unnest(idx.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON key_column.attnum > 0 JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = key_column.attnum WHERE namespace.nspname = `+renderer.Placeholder(1)+` AND relation.relname = `+renderer.Placeholder(2)+` AND NOT idx.indisprimary GROUP BY index_class.relname, idx.indisunique ORDER BY index_class.relname`, schema, table)
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var index persistencedriver.ModuleSchemaIndex
		var columns string
		if err := indexRows.Scan(&index.Name, &index.Unique, &columns); err != nil {
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		index.Columns = strings.Split(columns, ",")
		result.Indexes = append(result.Indexes, index)
	}
	return result, true, indexRows.Err()
}
