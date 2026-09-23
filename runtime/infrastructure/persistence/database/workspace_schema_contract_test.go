package database_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkspaceScopedSchemaHasRequiredDiscriminatorWithoutDefault(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-schema.db"), IntegrationSecretKey: "workspace-schema-contract-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	tables := workspaceColumnTables(t, store.DB())
	if len(tables) == 0 {
		t.Fatal("runtime schema must contain workspace-scoped tables")
	}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			rows, err := store.DB().Query(`PRAGMA table_info(` + store.Identifier(table) + `)`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			found := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue sql.NullString
				if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
					t.Fatal(err)
				}
				if name != "workspace_id" {
					continue
				}
				found = true
				if notNull != 1 {
					t.Errorf("%s.workspace_id must be NOT NULL", table)
				}
				if defaultValue.Valid {
					t.Errorf("%s.workspace_id must not have a default, got %q", table, defaultValue.String)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatalf("workspace inventory lost discriminator for %s", table)
			}
		})
	}
}

func TestWorkspaceScopedUniqueConstraintsIncludeWorkspaceUnlessGloballyOwned(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-unique.db"), IntegrationSecretKey: "workspace-unique-contract-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var violations []string
	for _, table := range workspaceColumnTables(t, store.DB()) {
		rows, err := store.DB().Query(`PRAGMA index_list(` + store.Identifier(table) + `)`)
		if err != nil {
			t.Fatal(err)
		}
		var uniqueIndexes []string
		for rows.Next() {
			var sequence, unique, partial int
			var indexName, origin string
			if err := rows.Scan(&sequence, &indexName, &unique, &origin, &partial); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if unique != 1 {
				continue
			}
			uniqueIndexes = append(uniqueIndexes, indexName)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, indexName := range uniqueIndexes {
			columns, err := sqliteIndexColumns(store.DB(), store.Identifier(indexName))
			if err != nil {
				t.Fatal(err)
			}
			if !containsString(columns, "workspace_id") && !globallyOwnedUniqueConstraint(table, indexName, columns) {
				violations = append(violations, fmt.Sprintf("%s.%s(%s)", table, indexName, strings.Join(columns, ",")))
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("workspace-scoped unique constraints missing workspace_id:\n%s", strings.Join(violations, "\n"))
	}
}

func TestWorkspaceAssociationsHaveScopedLookupIndexes(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-associations.db"), IntegrationSecretKey: "workspace-association-contract-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	expected := map[string][]string{
		"_workflow_node_instances": {"workspace_id", "process_id"},
		"_workflow_tasks":          {"workspace_id", "process_id"},
		"_workflow_process_events": {"workspace_id", "process_id"},
	}
	for table, prefix := range expected {
		if !sqliteTableHasIndexPrefix(t, store.DB(), store.Identifier(table), prefix) {
			t.Errorf("association %s must have workspace-scoped index prefix (%s)", table, strings.Join(prefix, ", "))
		}
	}
}

func sqliteTableHasIndexPrefix(t *testing.T, db *sql.DB, quotedTable string, prefix []string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + quotedTable + `)`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		columns, err := sqliteIndexColumns(db, `"`+strings.ReplaceAll(name, `"`, `""`)+`"`)
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) < len(prefix) {
			continue
		}
		matched := true
		for index := range prefix {
			matched = matched && columns[index] == prefix[index]
		}
		if matched {
			return true
		}
	}
	return false
}

func globallyOwnedUniqueConstraint(table, indexName string, columns []string) bool {
	if (table == sharedoperation.TableName || table == sharedoperation.BreakGlassTableName || table == sharedartifact.TableName || table == sharedartifact.BindingTableName) && len(columns) == 1 && columns[0] == "id" {
		return true
	}
	if len(columns) != 1 || columns[0] != "token_hash" {
		return false
	}
	return table == "_action_assurance_grants" && indexName == "uniq_action_assurance_token_hash"
}

func sqliteIndexColumns(db *sql.DB, quotedIndex string) ([]string, error) {
	rows, err := db.Query(`PRAGMA index_info(` + quotedIndex + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var sequence, cid int
		var name string
		if err := rows.Scan(&sequence, &cid, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func workspaceColumnTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'workspace_id' ORDER BY m.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(table) != "" {
			tables = append(tables, table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}
