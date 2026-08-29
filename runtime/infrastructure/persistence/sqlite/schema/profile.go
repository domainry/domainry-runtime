package schema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool        { return false }
func (Profile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (Profile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"}
}
func (Profile) WorkspaceTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'workspace_id' ORDER BY m.name"}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}

func (Profile) InspectModuleSchemaTable(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer, _ string, table string) (persistencedriver.ModuleSchemaTable, bool, error) {
	rows, err := database.QueryContext(ctx, "PRAGMA table_info("+renderer.Identifier(table)+")")
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	result := persistencedriver.ModuleSchemaTable{}
	for rows.Next() {
		var ordinal, notNull, primary int
		var name, physical string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &physical, &notNull, &defaultValue, &primary); err != nil {
			_ = rows.Close()
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		result.Columns = append(result.Columns, persistencedriver.ModuleSchemaColumn{Name: name, Physical: physical, Nullable: notNull == 0, PrimaryKey: primary > 0})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	_ = rows.Close()
	if len(result.Columns) == 0 {
		return persistencedriver.ModuleSchemaTable{}, false, nil
	}
	for index := range result.Columns {
		if !result.Columns[index].PrimaryKey || !result.Columns[index].Nullable {
			continue
		}
		var nulls int
		query := "SELECT COUNT(*) FROM " + renderer.Table(table) + " WHERE " + renderer.Identifier(result.Columns[index].Name) + " IS NULL"
		if err := database.QueryRowContext(ctx, query).Scan(&nulls); err != nil {
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		if nulls != 0 {
			return persistencedriver.ModuleSchemaTable{}, false, fmt.Errorf("primary key %s.%s contains %d NULL values", table, result.Columns[index].Name, nulls)
		}
		result.Columns[index].Nullable = false
	}
	indexRows, err := database.QueryContext(ctx, "PRAGMA index_list("+renderer.Identifier(table)+")")
	if err != nil {
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	indexes := []persistencedriver.ModuleSchemaIndex{}
	for indexRows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := indexRows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = indexRows.Close()
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		if origin != "pk" && !strings.HasPrefix(name, "sqlite_autoindex_") {
			indexes = append(indexes, persistencedriver.ModuleSchemaIndex{Name: name, Unique: unique == 1})
		}
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return persistencedriver.ModuleSchemaTable{}, false, err
	}
	_ = indexRows.Close()
	for _, index := range indexes {
		columns, err := sqliteModuleIndexColumns(ctx, database, renderer, index.Name)
		if err != nil {
			return persistencedriver.ModuleSchemaTable{}, false, err
		}
		index.Columns = columns
		result.Indexes = append(result.Indexes, index)
	}
	return result, true, nil
}

func sqliteModuleIndexColumns(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer, index string) ([]string, error) {
	rows, err := database.QueryContext(ctx, "PRAGMA index_info("+renderer.Identifier(index)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var rank, columnID int
		var name string
		if err := rows.Scan(&rank, &columnID, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}
