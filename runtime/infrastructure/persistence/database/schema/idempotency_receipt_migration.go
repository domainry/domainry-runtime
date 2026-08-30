package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type idempotencyReceiptMigrationSpec struct {
	table           string
	scopeColumns    []string
	backfillColumns []string
}

func prepareIdempotencyReceiptMigrations(ctx context.Context, store Store, specs ...idempotencyReceiptMigrationSpec) error {
	if len(specs) == 0 {
		return nil
	}
	blockedTables := []string{}
	duplicateScopes := 0
	details := []string{}
	for _, spec := range specs {
		if err := ensureIdempotencyMigrationColumns(ctx, store, spec); err != nil {
			return err
		}
		if err := backfillIdempotencyReceiptRows(ctx, store, spec); err != nil {
			return err
		}
		duplicates, err := findIdempotencyMigrationDuplicates(ctx, store, spec)
		if err != nil {
			return err
		}
		if len(duplicates) == 0 {
			continue
		}
		for _, duplicate := range duplicates {
			details = append(details, fmt.Sprintf("table=%s scope=%s receipt_ids=%s", spec.table, duplicate.scopeKey, strings.Join(duplicate.receiptIDs, "|")))
		}
		blockedTables = append(blockedTables, spec.table)
		duplicateScopes += len(duplicates)
	}
	if len(blockedTables) > 0 {
		return fmt.Errorf("idempotency migration blocked: tables=%s duplicate_scopes=%d details=[%s]", strings.Join(blockedTables, ","), duplicateScopes, strings.Join(details, "; "))
	}
	return nil
}

func ensureIdempotencyMigrationColumns(ctx context.Context, store Store, spec idempotencyReceiptMigrationSpec) error {
	text := store.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''"
	for _, column := range []string{"workspace_id", "idempotency_key", "request_fingerprint", "status"} {
		if err := store.EnsureRuntimeColumn(ctx, spec.table, column, text); err != nil {
			return fmt.Errorf("prepare idempotency migration column %s.%s: %w", spec.table, column, err)
		}
	}
	return nil
}

func backfillIdempotencyReceiptRows(ctx context.Context, store Store, spec idempotencyReceiptMigrationSpec) error {
	columns := append([]string{"id", "workspace_id", "idempotency_key", "request_fingerprint", "status"}, spec.scopeColumns...)
	rows, err := store.SchemaDB().QueryContext(ctx, "SELECT "+idempotencyMigrationIdentifiers(store, columns)+" FROM "+store.TableIdentifier(spec.table))
	if err != nil {
		return fmt.Errorf("read idempotency migration rows for %s: %w", spec.table, err)
	}
	type migrationRow struct {
		id     string
		values []string
	}
	values := []migrationRow{}
	for rows.Next() {
		raw := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return err
		}
		row := migrationRow{id: idempotencyMigrationString(raw[0]), values: make([]string, len(columns)-1)}
		for index := 1; index < len(raw); index++ {
			row.values[index-1] = idempotencyMigrationString(raw[index])
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, row := range values {
		if strings.TrimSpace(row.id) == "" {
			return fmt.Errorf("idempotency migration blocked: table=%s row has empty primary id", spec.table)
		}
		backfilled := append([]string(nil), row.values...)
		if strings.TrimSpace(backfilled[0]) == "" {
			backfilled[0] = "default"
		}
		for index := 1; index < len(backfilled); index++ {
			if strings.TrimSpace(backfilled[index]) != "" {
				continue
			}
			column := columns[index+1]
			switch column {
			case "status":
				backfilled[index] = "succeeded"
			default:
				if idempotencyMigrationContains(spec.backfillColumns, column) || column == "idempotency_key" || column == "request_fingerprint" {
					backfilled[index] = "legacy:" + column + ":" + row.id
				}
			}
		}
		if idempotencyMigrationStringsEqual(row.values, backfilled) {
			continue
		}
		assignments := make([]string, 0, len(backfilled))
		args := make([]any, 0, len(backfilled)+1)
		for index, value := range backfilled {
			assignments = append(assignments, store.Identifier(columns[index+1])+" = "+store.Placeholder(index+1))
			args = append(args, value)
		}
		args = append(args, row.id)
		query := "UPDATE " + store.TableIdentifier(spec.table) + " SET " + strings.Join(assignments, ", ") + " WHERE " + store.Identifier("id") + " = " + store.Placeholder(len(args))
		if _, err := store.SchemaDB().ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("backfill idempotency migration row %s.%s: %w", spec.table, row.id, err)
		}
	}
	return nil
}

type idempotencyMigrationDuplicate struct {
	scopeKey   string
	receiptIDs []string
}

func findIdempotencyMigrationDuplicates(ctx context.Context, store Store, spec idempotencyReceiptMigrationSpec) ([]idempotencyMigrationDuplicate, error) {
	columns := append([]string{"workspace_id"}, spec.scopeColumns...)
	columns = append(columns, "idempotency_key")
	groupColumns := idempotencyMigrationIdentifiers(store, columns)
	query := "SELECT " + groupColumns + ", COUNT(*) FROM " + store.TableIdentifier(spec.table) + " GROUP BY " + groupColumns + " HAVING COUNT(*) > 1"
	rows, err := store.SchemaDB().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("scan idempotency duplicate scopes for %s: %w", spec.table, err)
	}
	type duplicateScope struct{ values []string }
	scopes := []duplicateScope{}
	for rows.Next() {
		raw := make([]any, len(columns)+1)
		destinations := make([]any, len(raw))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			_ = rows.Close()
			return nil, err
		}
		scope := duplicateScope{values: make([]string, len(columns))}
		for index := range columns {
			scope.values[index] = idempotencyMigrationString(raw[index])
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	duplicates := make([]idempotencyMigrationDuplicate, 0, len(scopes))
	for _, scope := range scopes {
		where := make([]string, len(columns))
		args := make([]any, len(columns))
		for index, column := range columns {
			where[index] = store.Identifier(column) + " = " + store.Placeholder(index+1)
			args[index] = scope.values[index]
		}
		idRows, err := store.SchemaDB().QueryContext(ctx, "SELECT "+store.Identifier("id")+" FROM "+store.TableIdentifier(spec.table)+" WHERE "+strings.Join(where, " AND ")+" ORDER BY "+store.Identifier("id"), args...)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for idRows.Next() {
			var id string
			if err := idRows.Scan(&id); err != nil {
				_ = idRows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		if err := idRows.Err(); err != nil {
			_ = idRows.Close()
			return nil, err
		}
		_ = idRows.Close()
		scopeJSON, _ := json.Marshal(scope.values)
		duplicates = append(duplicates, idempotencyMigrationDuplicate{scopeKey: string(scopeJSON), receiptIDs: ids})
	}
	return duplicates, nil
}

func idempotencyMigrationIdentifiers(store Store, columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = store.Identifier(column)
	}
	return strings.Join(quoted, ", ")
}

func idempotencyMigrationString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func idempotencyMigrationStringsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func idempotencyMigrationContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
