package datamigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	drivercontract "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func inspectPostgres(ctx context.Context, db *sql.DB, schema string) (Inventory, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		schema = "public"
	}
	if !drivercontract.ValidSQLIdentifier(schema) {
		return Inventory{}, fmt.Errorf("unsafe PostgreSQL schema identifier %q", schema)
	}
	result := Inventory{Engine: EnginePostgres, Schema: schema, CapturedAt: time.Now().UTC()}
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE' ORDER BY table_name`, schema)
	if err != nil {
		return Inventory{}, fmt.Errorf("inventory PostgreSQL tables: %w", err)
	}
	tableNames := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return Inventory{}, err
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Inventory{}, err
	}
	_ = rows.Close()
	for _, name := range tableNames {
		table, err := inspectPostgresTable(ctx, db, schema, name)
		if err != nil {
			return Inventory{}, err
		}
		result.Tables = append(result.Tables, table)
		result.DatabaseBytes += table.EstimatedBytes
	}
	if err := inspectPostgresSequences(ctx, db, schema, &result); err != nil {
		return Inventory{}, err
	}
	if err := inspectPostgresViewsAndTriggers(ctx, db, schema, &result); err != nil {
		return Inventory{}, err
	}
	return result, nil
}

func inspectPostgresViewsAndTriggers(ctx context.Context, db *sql.DB, schema string, inventory *Inventory) error {
	views, err := db.QueryContext(ctx, `SELECT table_name, COALESCE(view_definition, '') FROM information_schema.views WHERE table_schema = $1 ORDER BY table_name`, schema)
	if err != nil {
		return fmt.Errorf("inventory PostgreSQL views: %w", err)
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
	triggers, err := db.QueryContext(ctx, `SELECT trigger_name, event_object_table, action_timing, event_manipulation, action_statement FROM information_schema.triggers WHERE trigger_schema = $1 ORDER BY trigger_name, event_manipulation`, schema)
	if err != nil {
		return fmt.Errorf("inventory PostgreSQL triggers: %w", err)
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

func inspectPostgresSequences(ctx context.Context, db *sql.DB, schema string, inventory *Inventory) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT sequence_class.relname, COALESCE(table_class.relname, ''), COALESCE(attribute.attname, '')
FROM pg_class sequence_class
JOIN pg_namespace namespace ON namespace.oid = sequence_class.relnamespace
LEFT JOIN pg_depend dependency ON dependency.objid = sequence_class.oid AND dependency.deptype IN ('a', 'i')
LEFT JOIN pg_class table_class ON table_class.oid = dependency.refobjid
LEFT JOIN pg_attribute attribute ON attribute.attrelid = table_class.oid AND attribute.attnum = dependency.refobjsubid
WHERE sequence_class.relkind = 'S' AND namespace.nspname = $1
ORDER BY sequence_class.relname`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence SequenceInventory
		if err := rows.Scan(&sequence.Name, &sequence.OwnedTable, &sequence.OwnedColumn); err != nil {
			return err
		}
		if !drivercontract.ValidSQLIdentifier(sequence.Name) {
			return fmt.Errorf("unsafe PostgreSQL sequence identifier %q", sequence.Name)
		}
		if err := db.QueryRowContext(ctx, "SELECT last_value FROM "+quote(schema)+"."+quote(sequence.Name)).Scan(&sequence.CurrentValue); err != nil {
			return err
		}
		inventory.Sequences = append(inventory.Sequences, sequence)
	}
	return rows.Err()
}

func inspectPostgresTable(ctx context.Context, db *sql.DB, schema, name string) (TableInventory, error) {
	if !drivercontract.ValidSQLIdentifier(name) {
		return TableInventory{}, fmt.Errorf("unsafe PostgreSQL table identifier %q", name)
	}
	relation := quote(schema) + "." + quote(name)
	table := TableInventory{Name: name}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+relation).Scan(&table.Rows); err != nil {
		return TableInventory{}, err
	}
	_ = db.QueryRowContext(ctx, `SELECT pg_total_relation_size($1::regclass)`, schema+"."+name).Scan(&table.EstimatedBytes)
	columns, err := db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable, COALESCE(column_default, '') FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`, schema, name)
	if err != nil {
		return TableInventory{}, err
	}
	for columns.Next() {
		var column ColumnInventory
		var nullable string
		if err := columns.Scan(&column.Name, &column.Type, &nullable, &column.Default); err != nil {
			_ = columns.Close()
			return TableInventory{}, err
		}
		column.Nullable = nullable == "YES"
		table.Columns = append(table.Columns, column)
		table.WorkspaceScoped = table.WorkspaceScoped || column.Name == "workspace_id"
		if column.Type == "bytea" {
			table.LargeObjectColumns = append(table.LargeObjectColumns, column.Name)
		}
	}
	if err := columns.Err(); err != nil {
		_ = columns.Close()
		return TableInventory{}, err
	}
	_ = columns.Close()
	pkRows, err := db.QueryContext(ctx, `SELECT a.attname FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary ORDER BY k.ord`, schema, name)
	if err != nil {
		return TableInventory{}, err
	}
	for pkRows.Next() {
		var column string
		if err := pkRows.Scan(&column); err != nil {
			_ = pkRows.Close()
			return TableInventory{}, err
		}
		table.PrimaryKey = append(table.PrimaryKey, column)
	}
	if err := pkRows.Err(); err != nil {
		_ = pkRows.Close()
		return TableInventory{}, err
	}
	_ = pkRows.Close()
	for position, primaryKey := range table.PrimaryKey {
		for columnIndex := range table.Columns {
			if table.Columns[columnIndex].Name == primaryKey {
				table.Columns[columnIndex].PrimaryKey = position + 1
			}
		}
	}
	if err := inspectPostgresIndexes(ctx, db, schema, &table); err != nil {
		return TableInventory{}, err
	}
	if err := inspectPostgresForeignKeys(ctx, db, schema, &table); err != nil {
		return TableInventory{}, err
	}
	constraints, err := inspectInformationSchemaConstraints(ctx, db, EnginePostgres, schema, name)
	if err != nil {
		return TableInventory{}, err
	}
	table.Constraints = constraints
	for _, column := range table.LargeObjectColumns {
		var maximum sql.NullInt64
		err := db.QueryRowContext(ctx, "SELECT MAX(OCTET_LENGTH("+quote(column)+")) FROM "+relation).Scan(&maximum)
		updateMaximumLargeObjectSize(&table.MaximumLargeObjectSize, maximum, err)
	}
	if table.WorkspaceScoped {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+relation+" WHERE "+quote("workspace_id")+" IS NULL OR BTRIM("+quote("workspace_id")+"::text) = ''").Scan(&table.InvalidWorkspaceRows); err != nil {
			return TableInventory{}, err
		}
	}
	return table, nil
}

func inspectPostgresIndexes(ctx context.Context, db *sql.DB, schema string, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, `SELECT index_class.relname, idx.indisunique, array_to_json(array_agg(attribute.attname ORDER BY key_column.ordinality))::text
FROM pg_index idx
JOIN pg_class table_class ON table_class.oid = idx.indrelid
JOIN pg_namespace namespace ON namespace.oid = table_class.relnamespace
JOIN pg_class index_class ON index_class.oid = idx.indexrelid
JOIN LATERAL unnest(idx.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON key_column.attnum > 0
JOIN pg_attribute attribute ON attribute.attrelid = table_class.oid AND attribute.attnum = key_column.attnum
WHERE namespace.nspname = $1 AND table_class.relname = $2
GROUP BY index_class.relname, idx.indisunique
ORDER BY index_class.relname`, schema, table.Name)
	if err != nil {
		return fmt.Errorf("inventory PostgreSQL indexes for %s: %w", table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var index IndexInventory
		var columnsJSON string
		if err := rows.Scan(&index.Name, &index.Unique, &columnsJSON); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(columnsJSON), &index.Columns); err != nil {
			return fmt.Errorf("decode PostgreSQL index columns for %s: %w", table.Name, err)
		}
		table.Indexes = append(table.Indexes, index)
	}
	return rows.Err()
}

func inspectPostgresForeignKeys(ctx context.Context, db *sql.DB, schema string, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, `SELECT constraint_name, column_name, foreign_table_name, foreign_column_name
FROM (
  SELECT con.conname AS constraint_name,
         source_column.attname AS column_name,
         foreign_table.relname AS foreign_table_name,
         foreign_column.attname AS foreign_column_name,
         source_key.ordinality
  FROM pg_constraint con
  JOIN pg_class source_table ON source_table.oid = con.conrelid
  JOIN pg_namespace namespace ON namespace.oid = source_table.relnamespace
  JOIN pg_class foreign_table ON foreign_table.oid = con.confrelid
  JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS source_key(attnum, ordinality) ON true
  JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS foreign_key(attnum, ordinality) ON foreign_key.ordinality = source_key.ordinality
  JOIN pg_attribute source_column ON source_column.attrelid = source_table.oid AND source_column.attnum = source_key.attnum
  JOIN pg_attribute foreign_column ON foreign_column.attrelid = foreign_table.oid AND foreign_column.attnum = foreign_key.attnum
  WHERE con.contype = 'f' AND namespace.nspname = $1 AND source_table.relname = $2
) relationships ORDER BY constraint_name, ordinality`, schema, table.Name)
	if err != nil {
		return fmt.Errorf("inventory PostgreSQL foreign keys for %s: %w", table.Name, err)
	}
	defer rows.Close()
	byName := map[string]*ForeignInventory{}
	order := []string{}
	for rows.Next() {
		var constraintName, column, referencedTable, referencedColumn string
		if err := rows.Scan(&constraintName, &column, &referencedTable, &referencedColumn); err != nil {
			return err
		}
		foreign := byName[constraintName]
		if foreign == nil {
			foreign = &ForeignInventory{ReferencedTable: referencedTable}
			byName[constraintName] = foreign
			order = append(order, constraintName)
		}
		foreign.Columns = append(foreign.Columns, column)
		foreign.ReferencedColumns = append(foreign.ReferencedColumns, referencedColumn)
	}
	for _, name := range order {
		table.ForeignKeys = append(table.ForeignKeys, *byName[name])
	}
	return rows.Err()
}
