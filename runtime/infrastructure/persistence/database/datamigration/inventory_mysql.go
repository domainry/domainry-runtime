package datamigration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	drivercontract "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func inspectMySQL(ctx context.Context, db *sql.DB, schema string) (Inventory, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		if err := db.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&schema); err != nil {
			return Inventory{}, fmt.Errorf("resolve MySQL schema: %w", err)
		}
	}
	if !drivercontract.ValidSQLIdentifier(schema) {
		return Inventory{}, fmt.Errorf("unsafe MySQL schema identifier %q", schema)
	}
	result := Inventory{Engine: EngineMySQL, Schema: schema, CapturedAt: time.Now().UTC()}
	rows, err := db.QueryContext(ctx, `SELECT table_name, COALESCE(data_length, 0) + COALESCE(index_length, 0) FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE' ORDER BY table_name`, schema)
	if err != nil {
		return Inventory{}, fmt.Errorf("inventory MySQL tables: %w", err)
	}
	type mysqlTable struct {
		name           string
		estimatedBytes int64
	}
	tableNames := []mysqlTable{}
	for rows.Next() {
		var name string
		var estimatedBytes int64
		if err := rows.Scan(&name, &estimatedBytes); err != nil {
			_ = rows.Close()
			return Inventory{}, err
		}
		tableNames = append(tableNames, mysqlTable{name: name, estimatedBytes: estimatedBytes})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Inventory{}, err
	}
	_ = rows.Close()
	for _, candidate := range tableNames {
		name, estimatedBytes := candidate.name, candidate.estimatedBytes
		table, err := inspectMySQLTable(ctx, db, schema, name)
		if err != nil {
			return Inventory{}, err
		}
		table.EstimatedBytes = estimatedBytes
		result.DatabaseBytes += estimatedBytes
		result.Tables = append(result.Tables, table)
	}
	if err := inspectMySQLViewsAndTriggers(ctx, db, schema, &result); err != nil {
		return Inventory{}, err
	}
	return result, nil
}

func inspectMySQLTable(ctx context.Context, db *sql.DB, schema, name string) (TableInventory, error) {
	if !drivercontract.ValidSQLIdentifier(name) {
		return TableInventory{}, fmt.Errorf("unsafe MySQL table identifier %q", name)
	}
	table := TableInventory{Name: name}
	relation := mysqlQuote(schema) + "." + mysqlQuote(name)
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+relation).Scan(&table.Rows); err != nil {
		return TableInventory{}, fmt.Errorf("count MySQL table %s: %w", name, err)
	}
	columns, err := db.QueryContext(ctx, `SELECT column_name, column_type, is_nullable, COALESCE(column_default, ''), column_key FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, name)
	if err != nil {
		return TableInventory{}, fmt.Errorf("inventory MySQL columns for %s: %w", name, err)
	}
	for columns.Next() {
		var column ColumnInventory
		var nullable, key string
		if err := columns.Scan(&column.Name, &column.Type, &nullable, &column.Default, &key); err != nil {
			_ = columns.Close()
			return TableInventory{}, err
		}
		column.Nullable = nullable == "YES"
		if key == "PRI" {
			column.PrimaryKey = len(table.PrimaryKey) + 1
			table.PrimaryKey = append(table.PrimaryKey, column.Name)
		}
		table.WorkspaceScoped = table.WorkspaceScoped || column.Name == "workspace_id"
		if strings.Contains(strings.ToLower(column.Type), "blob") || strings.Contains(strings.ToLower(column.Type), "binary") {
			table.LargeObjectColumns = append(table.LargeObjectColumns, column.Name)
		}
		table.Columns = append(table.Columns, column)
	}
	if err := columns.Err(); err != nil {
		_ = columns.Close()
		return TableInventory{}, err
	}
	_ = columns.Close()
	if err := inspectMySQLIndexes(ctx, db, schema, &table); err != nil {
		return TableInventory{}, err
	}
	if err := inspectMySQLForeignKeys(ctx, db, schema, &table); err != nil {
		return TableInventory{}, err
	}
	constraints, err := inspectInformationSchemaConstraints(ctx, db, EngineMySQL, schema, name)
	if err != nil {
		return TableInventory{}, err
	}
	table.Constraints = constraints
	if table.WorkspaceScoped {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+relation+" WHERE "+mysqlQuote("workspace_id")+" IS NULL OR TRIM(CAST("+mysqlQuote("workspace_id")+" AS CHAR)) = ''").Scan(&table.InvalidWorkspaceRows); err != nil {
			return TableInventory{}, err
		}
	}
	for _, column := range table.LargeObjectColumns {
		var maximum sql.NullInt64
		err := db.QueryRowContext(ctx, "SELECT MAX(OCTET_LENGTH("+mysqlQuote(column)+")) FROM "+relation).Scan(&maximum)
		updateMaximumLargeObjectSize(&table.MaximumLargeObjectSize, maximum, err)
	}
	return table, nil
}

func updateMaximumLargeObjectSize(current *int64, maximum sql.NullInt64, err error) {
	if err == nil && maximum.Valid && maximum.Int64 > *current {
		*current = maximum.Int64
	}
}

func inspectMySQLIndexes(ctx context.Context, db *sql.DB, schema string, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, `SELECT index_name, non_unique, column_name FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? ORDER BY index_name, seq_in_index`, schema, table.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]*IndexInventory{}
	order := []string{}
	for rows.Next() {
		var name, column string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &column); err != nil {
			return err
		}
		index := byName[name]
		if index == nil {
			index = &IndexInventory{Name: name, Unique: nonUnique == 0}
			byName[name] = index
			order = append(order, name)
		}
		index.Columns = append(index.Columns, column)
	}
	for _, name := range order {
		table.Indexes = append(table.Indexes, *byName[name])
	}
	return rows.Err()
}

func inspectMySQLForeignKeys(ctx context.Context, db *sql.DB, schema string, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, `SELECT constraint_name, column_name, referenced_table_name, referenced_column_name FROM information_schema.key_column_usage WHERE table_schema = ? AND table_name = ? AND referenced_table_name IS NOT NULL ORDER BY constraint_name, ordinal_position`, schema, table.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]*ForeignInventory{}
	order := []string{}
	for rows.Next() {
		var name, column, referencedTable, referencedColumn string
		if err := rows.Scan(&name, &column, &referencedTable, &referencedColumn); err != nil {
			return err
		}
		foreign := byName[name]
		if foreign == nil {
			foreign = &ForeignInventory{ReferencedTable: referencedTable}
			byName[name] = foreign
			order = append(order, name)
		}
		foreign.Columns = append(foreign.Columns, column)
		foreign.ReferencedColumns = append(foreign.ReferencedColumns, referencedColumn)
	}
	for _, name := range order {
		table.ForeignKeys = append(table.ForeignKeys, *byName[name])
	}
	return rows.Err()
}

func inspectMySQLViewsAndTriggers(ctx context.Context, db *sql.DB, schema string, inventory *Inventory) error {
	views, err := db.QueryContext(ctx, `SELECT table_name, COALESCE(view_definition, '') FROM information_schema.views WHERE table_schema = ? ORDER BY table_name`, schema)
	if err != nil {
		return err
	}
	for views.Next() {
		var view ViewInventory
		if err := views.Scan(&view.Name, &view.Definition); err != nil {
			_ = views.Close()
			return err
		}
		inventory.Views = append(inventory.Views, view)
	}
	if err := views.Err(); err != nil {
		_ = views.Close()
		return err
	}
	_ = views.Close()
	triggers, err := db.QueryContext(ctx, `SELECT trigger_name, event_object_table, action_timing, event_manipulation, action_statement FROM information_schema.triggers WHERE trigger_schema = ? ORDER BY trigger_name, event_manipulation`, schema)
	if err != nil {
		return err
	}
	defer triggers.Close()
	for triggers.Next() {
		var trigger TriggerInventory
		if err := triggers.Scan(&trigger.Name, &trigger.Table, &trigger.Timing, &trigger.Event, &trigger.Definition); err != nil {
			return err
		}
		inventory.Triggers = append(inventory.Triggers, trigger)
	}
	return triggers.Err()
}

func inspectInformationSchemaConstraints(ctx context.Context, db *sql.DB, engine Engine, schema, table string) ([]ConstraintInventory, error) {
	placeholder := "$1"
	second := "$2"
	if engine == EngineMySQL {
		placeholder, second = "?", "?"
	}
	query := `SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema = ` + placeholder + ` AND table_name = ` + second + ` ORDER BY constraint_name`
	rows, err := db.QueryContext(ctx, query, schema, table)
	if err != nil {
		return nil, fmt.Errorf("inventory %s constraints for %s: %w", engine, table, err)
	}
	defer rows.Close()
	constraints := []ConstraintInventory{}
	for rows.Next() {
		var constraint ConstraintInventory
		if err := rows.Scan(&constraint.Name, &constraint.Type); err != nil {
			return nil, err
		}
		constraints = append(constraints, constraint)
	}
	return constraints, rows.Err()
}
