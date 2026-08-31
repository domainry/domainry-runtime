package database_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// TestWorkspaceOwnerPhysicalIsolationAcrossDialects complements every owner's
// Repository A/B contract with a real-schema matrix. It proves that the same
// business identity can be inserted in workspaces A and B and that scoped
// update/delete operations cannot affect the peer tenant on every SQL engine.
func TestWorkspaceOwnerPhysicalIsolationAcrossDialects(t *testing.T) {
	dialects := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
	}
	owners := []struct{ owner, table, mutationColumn string }{
		{owner: "audit", table: "_audit_events", mutationColumn: "summary"},
		{owner: "automation", table: "_automation_rule_executions", mutationColumn: "candidate_json"},
		{owner: "integration", table: "_publication_outbox", mutationColumn: "payload_json"},
		{owner: "record", table: "_record_localized_values", mutationColumn: "text_value"},
		{owner: "workflow", table: "_workflow_executions", mutationColumn: "result_json"},
	}
	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: dialect.driver, DatabaseDSN: strings.TrimSpace(os.Getenv(dialect.dsnEnv)), IntegrationSecretKey: "workspace-owner-dialect-matrix"}
			if dialect.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "workspace-owner-matrix.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", dialect.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			prefix := fmt.Sprintf("wsm%d", time.Now().UTC().UnixNano())
			for _, owner := range owners {
				t.Run(owner.owner, func(t *testing.T) {
					assertPhysicalWorkspacePair(t, store, owner.table, owner.mutationColumn, prefix+owner.owner)
				})
			}
		})
	}
}

func assertPhysicalWorkspacePair(t *testing.T, store *database.RuntimeStore, table, mutationColumn, identity string) {
	t.Helper()
	workspaceA, workspaceB := identity+"a", identity+"b"
	columns, err := workspaceMatrixColumns(t.Context(), store, table)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := columns["workspace_id"]; !ok {
		t.Fatalf("%s has no workspace_id", table)
	}
	ordered := make([]string, 0, len(columns))
	for name := range columns {
		ordered = append(ordered, name)
	}
	// Column order is stable because the schema query returns physical order.
	ordered = workspaceMatrixPhysicalOrder(t.Context(), store, table)
	insert := func(workspace string) error {
		quoted, placeholders, args := make([]string, 0, len(ordered)), make([]string, 0, len(ordered)), make([]any, 0, len(ordered))
		for index, column := range ordered {
			quoted = append(quoted, store.Identifier(column))
			placeholders = append(placeholders, store.Placeholder(index+1))
			args = append(args, workspaceMatrixValue(column, columns[column], workspace, identity))
		}
		_, err := store.DB().ExecContext(t.Context(), "INSERT INTO "+store.TableIdentifier(table)+" ("+strings.Join(quoted, ", ")+") VALUES ("+strings.Join(placeholders, ", ")+")", args...)
		return err
	}
	defer func() {
		_, _ = store.DB().ExecContext(context.Background(), "DELETE FROM "+store.TableIdentifier(table)+" WHERE "+store.Identifier("workspace_id")+" IN ("+store.Placeholder(1)+", "+store.Placeholder(2)+")", workspaceA, workspaceB)
	}()
	if err := insert(workspaceA); err != nil {
		t.Fatalf("insert workspace A into %s: %v", table, err)
	}
	if err := insert(workspaceB); err != nil {
		t.Fatalf("insert same identity in workspace B into %s: %v", table, err)
	}
	result, err := store.DB().ExecContext(t.Context(), "UPDATE "+store.TableIdentifier(table)+" SET "+store.Identifier(mutationColumn)+" = "+store.Placeholder(1)+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(2), "matrix-mutated-a", workspaceA)
	if err != nil {
		t.Fatalf("update workspace A in %s: %v", table, err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		t.Fatalf("update workspace A in %s affected %d rows", table, affected)
	}
	var peerValue string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT "+store.Identifier(mutationColumn)+" FROM "+store.TableIdentifier(table)+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspaceB).Scan(&peerValue); err != nil {
		t.Fatalf("read workspace B in %s: %v", table, err)
	}
	if peerValue == "matrix-mutated-a" {
		t.Fatalf("workspace A update crossed into workspace B for %s", table)
	}
	result, err = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier(table)+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspaceA)
	if err != nil {
		t.Fatalf("delete workspace A from %s: %v", table, err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		t.Fatalf("delete workspace A from %s affected %d rows", table, affected)
	}
	var remaining int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier(table)+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspaceB).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("workspace B row lost after deleting A from %s: remaining=%d err=%v", table, remaining, err)
	}
}

func workspaceMatrixColumns(ctx context.Context, store *database.RuntimeStore, table string) (map[string]string, error) {
	rows, err := store.DB().QueryContext(ctx, "SELECT * FROM "+store.TableIdentifier(table)+" WHERE 1 = 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(types))
	for _, column := range types {
		result[column.Name()] = strings.ToUpper(column.DatabaseTypeName())
	}
	return result, nil
}

func workspaceMatrixPhysicalOrder(ctx context.Context, store *database.RuntimeStore, table string) []string {
	rows, err := store.DB().QueryContext(ctx, "SELECT * FROM "+store.TableIdentifier(table)+" WHERE 1 = 0")
	if err != nil {
		return nil
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	return columns
}

func workspaceMatrixValue(column, databaseType, workspace, identity string) any {
	switch column {
	case "workspace_id":
		return workspace
	case "id", "request_id":
		return identity
	}
	if strings.Contains(column, "json") {
		return "{}"
	}
	if strings.Contains(databaseType, "INT") || strings.Contains(databaseType, "NUMERIC") || strings.Contains(databaseType, "DECIMAL") {
		return 1
	}
	if strings.Contains(databaseType, "BOOL") {
		return false
	}
	if strings.Contains(databaseType, "BLOB") || strings.Contains(databaseType, "BINARY") || strings.Contains(databaseType, "BYTEA") {
		return []byte("matrix")
	}
	if column == "status" {
		return "queued"
	}
	return identity + "_" + column
}
