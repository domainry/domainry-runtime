package datamigration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	drivercontract "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func Inspect(ctx context.Context, db *sql.DB, engine Engine, schema string) (Inventory, error) {
	if db == nil {
		return Inventory{}, fmt.Errorf("database migration inventory requires a database")
	}
	if err := ctx.Err(); err != nil {
		return Inventory{}, err
	}
	switch engine {
	case EngineSQLite:
		return inspectSQLite(ctx, db)
	case EnginePostgres:
		return inspectPostgres(ctx, db, schema)
	case EngineMySQL:
		return inspectMySQL(ctx, db, schema)
	default:
		return Inventory{}, fmt.Errorf("unsupported inventory engine %q", engine)
	}
}

func inspectSQLite(ctx context.Context, db *sql.DB) (Inventory, error) {
	result := Inventory{Engine: EngineSQLite, CapturedAt: time.Now().UTC()}
	var pageCount, pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err == nil {
		_ = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)
		result.DatabaseBytes = pageCount * pageSize
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return Inventory{}, fmt.Errorf("inventory SQLite tables: %w", err)
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
		table, err := inspectSQLiteTable(ctx, db, name)
		if err != nil {
			return Inventory{}, err
		}
		result.Tables = append(result.Tables, table)
	}
	if err := inspectSQLiteSequences(ctx, db, &result); err != nil {
		return Inventory{}, err
	}
	if err := inspectSQLiteViewsAndTriggers(ctx, db, &result); err != nil {
		return Inventory{}, err
	}
	return result, nil
}

func inspectSQLiteViewsAndTriggers(ctx context.Context, db *sql.DB, inventory *Inventory) error {
	rows, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('view', 'trigger') ORDER BY type, name`)
	if err != nil {
		return fmt.Errorf("inventory SQLite views and triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name, table, definition string
		if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
			return err
		}
		if kind == "view" {
			inventory.Views = append(inventory.Views, ViewInventory{Name: name, Definition: definition})
		} else {
			inventory.Triggers = append(inventory.Triggers, TriggerInventory{Name: name, Table: table, Definition: definition})
		}
	}
	return rows.Err()
}

func inspectSQLiteSequences(ctx context.Context, db *sql.DB, inventory *Inventory) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT name, seq FROM sqlite_sequence ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence SequenceInventory
		if err := rows.Scan(&sequence.OwnedTable, &sequence.CurrentValue); err != nil {
			return err
		}
		sequence.Name = sequence.OwnedTable
		for _, table := range inventory.Tables {
			if table.Name != sequence.OwnedTable {
				continue
			}
			for _, column := range table.Columns {
				if column.PrimaryKey > 0 && isIntegerType(normalizeType(column.Type)) {
					sequence.OwnedColumn = column.Name
					break
				}
			}
		}
		inventory.Sequences = append(inventory.Sequences, sequence)
	}
	return rows.Err()
}

func inspectSQLiteTable(ctx context.Context, db *sql.DB, name string) (TableInventory, error) {
	if !drivercontract.ValidSQLIdentifier(name) {
		return TableInventory{}, fmt.Errorf("unsafe SQLite table identifier %q", name)
	}
	quoted := quote(name)
	table := TableInventory{Name: name}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoted).Scan(&table.Rows); err != nil {
		return TableInventory{}, fmt.Errorf("count SQLite table %s: %w", name, err)
	}
	columns, err := db.QueryContext(ctx, "PRAGMA table_info("+quoted+")")
	if err != nil {
		return TableInventory{}, fmt.Errorf("inventory SQLite columns for %s: %w", name, err)
	}
	blobColumns := []string{}
	for columns.Next() {
		var ordinal, notNull, primaryKey int
		var columnName, declaredType string
		var defaultValue sql.NullString
		if err := columns.Scan(&ordinal, &columnName, &declaredType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = columns.Close()
			return TableInventory{}, err
		}
		column := ColumnInventory{Name: columnName, Type: strings.ToUpper(strings.TrimSpace(declaredType)), Nullable: notNull == 0, PrimaryKey: primaryKey}
		if defaultValue.Valid {
			column.Default = defaultValue.String
		}
		table.Columns = append(table.Columns, column)
		if primaryKey > 0 {
			table.PrimaryKey = append(table.PrimaryKey, columnName)
		}
		if columnName == "workspace_id" {
			table.WorkspaceScoped = true
		}
		if strings.Contains(column.Type, "BLOB") {
			table.LargeObjectColumns = append(table.LargeObjectColumns, columnName)
			blobColumns = append(blobColumns, columnName)
		}
	}
	if err := columns.Err(); err != nil {
		_ = columns.Close()
		return TableInventory{}, err
	}
	_ = columns.Close()
	for _, columnName := range blobColumns {
		var maximum sql.NullInt64
		err := db.QueryRowContext(ctx, "SELECT MAX(LENGTH("+quote(columnName)+")) FROM "+quoted).Scan(&maximum)
		updateMaximumLargeObjectSize(&table.MaximumLargeObjectSize, maximum, err)
	}
	sort.Slice(table.PrimaryKey, func(i, j int) bool {
		return primaryKeyPosition(table.Columns, table.PrimaryKey[i]) < primaryKeyPosition(table.Columns, table.PrimaryKey[j])
	})
	if table.WorkspaceScoped {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoted+" WHERE "+quote("workspace_id")+" IS NULL OR TRIM(CAST("+quote("workspace_id")+" AS TEXT)) = ''").Scan(&table.InvalidWorkspaceRows); err != nil {
			return TableInventory{}, fmt.Errorf("validate workspace scope for %s: %w", name, err)
		}
	}
	if err := inspectSQLiteIndexes(ctx, db, &table); err != nil {
		return TableInventory{}, err
	}
	if err := inspectSQLiteForeignKeys(ctx, db, &table); err != nil {
		return TableInventory{}, err
	}
	table.Constraints = append(table.Constraints, sqliteConstraintInventory(table)...)
	return table, nil
}

func sqliteConstraintInventory(table TableInventory) []ConstraintInventory {
	constraints := make([]ConstraintInventory, 0, 1+len(table.Indexes)+len(table.ForeignKeys))
	if len(table.PrimaryKey) > 0 {
		constraints = append(constraints, ConstraintInventory{Name: "primary_key", Type: "PRIMARY KEY", Columns: append([]string(nil), table.PrimaryKey...)})
	}
	for _, index := range table.Indexes {
		if index.Unique {
			constraints = append(constraints, ConstraintInventory{Name: index.Name, Type: "UNIQUE", Columns: append([]string(nil), index.Columns...)})
		}
	}
	for index, foreign := range table.ForeignKeys {
		constraints = append(constraints, ConstraintInventory{Name: fmt.Sprintf("foreign_key_%d", index+1), Type: "FOREIGN KEY", Columns: append([]string(nil), foreign.Columns...), Definition: foreign.ReferencedTable + "(" + strings.Join(foreign.ReferencedColumns, ",") + ")"})
	}
	return constraints
}

func inspectSQLiteIndexes(ctx context.Context, db *sql.DB, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, "PRAGMA index_list("+quote(table.Name)+")")
	if err != nil {
		return err
	}
	indexes := []IndexInventory{}
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			return err
		}
		if !drivercontract.ValidSQLIdentifier(name) {
			return fmt.Errorf("unsafe SQLite index identifier %q", name)
		}
		indexes = append(indexes, IndexInventory{Name: name, Unique: unique == 1})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, indexValue := range indexes {
		index := indexValue
		name := index.Name
		columnRows, err := db.QueryContext(ctx, "PRAGMA index_info("+quote(name)+")")
		if err != nil {
			return err
		}
		for columnRows.Next() {
			var rank, columnID int
			var column string
			if err := columnRows.Scan(&rank, &columnID, &column); err != nil {
				_ = columnRows.Close()
				return err
			}
			index.Columns = append(index.Columns, column)
		}
		if err := columnRows.Err(); err != nil {
			_ = columnRows.Close()
			return err
		}
		_ = columnRows.Close()
		table.Indexes = append(table.Indexes, index)
	}
	return nil
}

func inspectSQLiteForeignKeys(ctx context.Context, db *sql.DB, table *TableInventory) error {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_list("+quote(table.Name)+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	foreignByID := map[int]*ForeignInventory{}
	order := []int{}
	for rows.Next() {
		var id, sequence int
		var referencedTable, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &referencedTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return err
		}
		foreign := foreignByID[id]
		if foreign == nil {
			foreign = &ForeignInventory{ReferencedTable: referencedTable}
			foreignByID[id] = foreign
			order = append(order, id)
		}
		foreign.Columns = append(foreign.Columns, from)
		foreign.ReferencedColumns = append(foreign.ReferencedColumns, to)
	}
	sort.Ints(order)
	for _, id := range order {
		table.ForeignKeys = append(table.ForeignKeys, *foreignByID[id])
	}
	return rows.Err()
}
