package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
)

var inspectSQLiteRetirementTransaction = inspectSQLiteTransaction

func verifySQLiteColumnDropPreservesSchema(ctx context.Context, tx *sql.Tx, before datamigration.Inventory, object operationsmodel.DatabaseObjectIdentity) error {
	after, err := inspectSQLiteRetirementTransaction(ctx, tx)
	if err != nil {
		return err
	}
	beforeTable, beforeFound := retirementTable(before.Tables, object)
	afterTable, afterFound := retirementTable(after.Tables, object)
	if !beforeFound || !afterFound || retirementColumnExists(afterTable.Columns, object.Name) {
		return fmt.Errorf("SQLite column retirement did not preserve target table")
	}
	for _, index := range beforeTable.Indexes {
		if stringSliceContains(index.Columns, object.Name) {
			continue
		}
		if !retirementIndexExists(afterTable.Indexes, index.Name) {
			return fmt.Errorf("SQLite column retirement removed unaffected index %s", index.Name)
		}
	}
	if len(beforeTable.ForeignKeys) != len(afterTable.ForeignKeys) {
		return fmt.Errorf("SQLite column retirement changed foreign key semantics")
	}
	for _, trigger := range before.Triggers {
		if trigger.Table == object.ParentName && !strings.Contains(strings.ToLower(trigger.Definition), strings.ToLower(object.Name)) && !retirementTriggerExists(after.Triggers, trigger.Name) {
			return fmt.Errorf("SQLite column retirement removed unaffected trigger %s", trigger.Name)
		}
	}
	return nil
}

func inspectSQLiteTransaction(ctx context.Context, tx *sql.Tx) (datamigration.Inventory, error) {
	// A temporary dedicated connection cannot observe uncommitted SQLite DDL;
	// collect only the preservation facts needed by the transaction.
	result := datamigration.Inventory{Engine: datamigration.EngineSQLite}
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return result, err
		}
		table := datamigration.TableInventory{Name: name}
		columns, err := tx.QueryContext(ctx, `PRAGMA table_info("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
		if err != nil {
			_ = rows.Close()
			return result, err
		}
		for columns.Next() {
			var ordinal, notNull, primaryKey int
			var column datamigration.ColumnInventory
			var defaultValue sql.NullString
			if err := columns.Scan(&ordinal, &column.Name, &column.Type, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = columns.Close()
				_ = rows.Close()
				return result, err
			}
			column.Nullable, column.PrimaryKey = notNull == 0, primaryKey
			table.Columns = append(table.Columns, column)
		}
		if err := columns.Err(); err != nil {
			_ = columns.Close()
			_ = rows.Close()
			return result, err
		}
		_ = columns.Close()
		indexRows, err := tx.QueryContext(ctx, `PRAGMA index_list("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
		if err != nil {
			_ = rows.Close()
			return result, err
		}
		for indexRows.Next() {
			var sequence, unique, partial int
			var index datamigration.IndexInventory
			var origin string
			if err := indexRows.Scan(&sequence, &index.Name, &unique, &origin, &partial); err != nil {
				_ = indexRows.Close()
				_ = rows.Close()
				return result, err
			}
			index.Unique = unique == 1
			table.Indexes = append(table.Indexes, index)
		}
		if err := indexRows.Err(); err != nil {
			_ = indexRows.Close()
			_ = rows.Close()
			return result, err
		}
		_ = indexRows.Close()
		result.Tables = append(result.Tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()
	triggerRows, err := tx.QueryContext(ctx, `SELECT name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		return result, err
	}
	for triggerRows.Next() {
		var trigger datamigration.TriggerInventory
		if err := triggerRows.Scan(&trigger.Name, &trigger.Table, &trigger.Definition); err != nil {
			_ = triggerRows.Close()
			return result, err
		}
		result.Triggers = append(result.Triggers, trigger)
	}
	if err := triggerRows.Err(); err != nil {
		_ = triggerRows.Close()
		return result, err
	}
	_ = triggerRows.Close()
	return result, nil
}

func retirementTable(tables []datamigration.TableInventory, object operationsmodel.DatabaseObjectIdentity) (datamigration.TableInventory, bool) {
	name := object.Name
	if object.Kind != "table" {
		name = object.ParentName
	}
	for _, table := range tables {
		if table.Name == name {
			return table, true
		}
	}
	return datamigration.TableInventory{}, false
}

func retirementColumnExists(columns []datamigration.ColumnInventory, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func retirementIndexExists(indexes []datamigration.IndexInventory, name string) bool {
	for _, index := range indexes {
		if index.Name == name {
			return true
		}
	}
	return false
}

func retirementTriggerExists(triggers []datamigration.TriggerInventory, name string) bool {
	for _, trigger := range triggers {
		if trigger.Name == name {
			return true
		}
	}
	return false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func databaseObjectNames(values []operationsmodel.DatabaseObjectIdentity) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Kind + ":" + value.Name
	}
	return result
}

func sameDatabaseEngine(driver string, engine datamigration.Engine) bool {
	driver = strings.ToLower(strings.TrimSpace(driver))
	return (engine == datamigration.EngineSQLite && driver == "sqlite") || (engine == datamigration.EnginePostgres && (driver == "postgres" || driver == "pgx")) || (engine == datamigration.EngineMySQL && driver == "mysql")
}
