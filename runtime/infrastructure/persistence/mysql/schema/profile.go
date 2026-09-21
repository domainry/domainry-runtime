package schema

import (
	"context"
	"regexp"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

var textDefaultLiteral = regexp.MustCompile(`TEXT NOT NULL DEFAULT ('(?:''|[^'])*')`)

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool { return true }
func (Profile) ColumnDefinition(definition string) string {
	definition = strings.TrimSpace(definition)
	return textDefaultLiteral.ReplaceAllString(definition, "TEXT NOT NULL DEFAULT ($1)")
}
func (Profile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"}
}
func (Profile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + renderer.Placeholder(1), Arguments: []any{table}}
}

func (Profile) InspectModuleSchemaTable(ctx context.Context, database persistencedriver.SchemaDatabase, _ ormdialect.Renderer, databaseSchema, table string) (persistencedriver.ModuleSchemaTable, bool, error) {
	schema := strings.TrimSpace(databaseSchema)
	if schema == "" {
		if err := database.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
	}
	rows, err := database.QueryContext(ctx, `SELECT column_name, column_type, is_nullable, column_key FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	result := persistencedriver.ModuleSchemaTable{}
	for rows.Next() {
		var column persistencedriver.ModuleSchemaColumn
		var nullable, key string
		if err := rows.Scan(&column.Name, &column.Physical, &nullable, &key); err != nil {
			_ = rows.Close()
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		column.Nullable, column.PrimaryKey = nullable == "YES", key == "PRI"
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
	indexRows, err := database.QueryContext(ctx, `SELECT index_name, non_unique, column_name FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? AND index_name <> 'PRIMARY' ORDER BY index_name, seq_in_index`, schema, table)
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	defer indexRows.Close()
	byName := map[string]*persistencedriver.ModuleSchemaIndex{}
	order := []string{}
	for indexRows.Next() {
		var name, column string
		var nonUnique int
		if err := indexRows.Scan(&name, &nonUnique, &column); err != nil {
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		index := byName[name]
		if index == nil {
			index = &persistencedriver.ModuleSchemaIndex{Name: name, Unique: nonUnique == 0}
			byName[name] = index
			order = append(order, name)
		}
		index.Columns = append(index.Columns, column)
	}
	if err := indexRows.Err(); err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	for _, name := range order {
		result.Indexes = append(result.Indexes, *byName[name])
	}
	return result, true, nil
}
