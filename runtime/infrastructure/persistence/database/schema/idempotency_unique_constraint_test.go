package schema_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIdempotencyColumnsRequireUniqueDatabaseConstraint(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "idempotency-constraints.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	reviewedLegacy := map[string]bool{"_workflow_executions.idempotency_key": true}
	for _, table := range tables {
		columns, err := sqliteTableColumns(store, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			if column != "dedup_key" && !strings.HasSuffix(column, "idempotency_key") {
				continue
			}
			identity := table + "." + column
			if reviewedLegacy[identity] {
				continue
			}
			unique, err := sqliteColumnHasUniqueIndex(store, table, column)
			if err != nil {
				t.Fatal(err)
			}
			if !unique {
				t.Errorf("idempotency schema violation: entry=%s owner=%s file=runtime/infrastructure/persistence/database/schema missing_contract=UNIQUE(%s)", identity, idempotencyTableOwner(table), column)
			}
		}
	}
}

func sqliteTableColumns(store *database.RuntimeStore, table string) ([]string, error) {
	rows, err := store.DB().Query(`PRAGMA table_info(` + store.Identifier(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func sqliteColumnHasUniqueIndex(store *database.RuntimeStore, table, targetColumn string) (bool, error) {
	rows, err := store.DB().Query(`PRAGMA index_list(` + store.Identifier(table) + `)`)
	if err != nil {
		return false, err
	}
	type indexState struct {
		name   string
		unique bool
	}
	var indexes []indexState
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return false, err
		}
		indexes = append(indexes, indexState{name: name, unique: unique == 1})
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, index := range indexes {
		if !index.unique {
			continue
		}
		indexRows, err := store.DB().Query(`PRAGMA index_info(` + store.Identifier(index.name) + `)`)
		if err != nil {
			return false, err
		}
		for indexRows.Next() {
			var sequence, columnSequence int
			var column string
			if err := indexRows.Scan(&sequence, &columnSequence, &column); err != nil {
				_ = indexRows.Close()
				return false, err
			}
			if column == targetColumn {
				_ = indexRows.Close()
				return true, nil
			}
		}
		if err := indexRows.Close(); err != nil {
			return false, err
		}
	}
	return false, nil
}

func idempotencyTableOwner(table string) string {
	for prefix, owner := range map[string]string{"business_action": "action", "record_mutation": "record", "workflow": "workflow", "automation": "automation", "integration": "integration", "business_change_plan": "changeplan", "auth_mutation": "auth"} {
		if strings.HasPrefix(table, prefix) {
			return owner
		}
	}
	return fmt.Sprintf("table:%s", table)
}
