package database

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ValidateLegacyWorkspaceScopes inventories legacy tenant rows without
// rewriting them. Missing and legacy-default scopes block the migration with
// deterministic details; the migration path never guesses a replacement.
func (s *RuntimeStore) ValidateLegacyWorkspaceScopes(ctx context.Context) error {
	db := s.schemaDatabase()
	tables, err := s.inventoryWorkspaceTables(ctx, db)
	if err != nil {
		return err
	}
	findings := []string{}
	for _, table := range tables {
		query := "SELECT COALESCE(" + s.identifier("workspace_id") + ", ''), COUNT(*) FROM " + s.tableIdentifier(table) +
			" WHERE " + s.identifier("workspace_id") + " IS NULL OR TRIM(" + s.identifier("workspace_id") + ") = ''" +
			" OR LOWER(TRIM(" + s.identifier("workspace_id") + ")) = 'default'" +
			" GROUP BY " + s.identifier("workspace_id")
		rows, queryErr := db.QueryContext(ctx, query)
		if queryErr != nil {
			return fmt.Errorf("inspect legacy workspace values for %s: %w", table, queryErr)
		}
		findingsByClassification := make(map[string]int64)
		for rows.Next() {
			var observed string
			var count int64
			if err := rows.Scan(&observed, &count); err != nil {
				rows.Close()
				return err
			}
			classification := "missing_workspace"
			if strings.EqualFold(strings.TrimSpace(observed), "default") {
				classification = "legacy_default_workspace"
			}
			findingsByClassification[classification] += count
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		classifications := make([]string, 0, len(findingsByClassification))
		for classification := range findingsByClassification {
			classifications = append(classifications, classification)
		}
		sort.Strings(classifications)
		for _, classification := range classifications {
			findings = append(findings, fmt.Sprintf("table=%s classification=%s row_count=%d", table, classification, findingsByClassification[classification]))
		}
	}
	if len(findings) > 0 {
		return fmt.Errorf("workspace scope migration blocked: manual source-data adjudication required: %s", strings.Join(findings, "; "))
	}
	return nil
}

func (s *RuntimeStore) inventoryWorkspaceTables(ctx context.Context, db schemaDatabase) ([]string, error) {
	base := s.sqlBase()
	query := base.RuntimeEngine.WorkspaceTablesQuery(base.SQLRenderer, base.DatabaseSchema)
	if strings.TrimSpace(query.Statement) == "" {
		return nil, fmt.Errorf("database engine does not support workspace table introspection")
	}
	rows, err := db.QueryContext(ctx, query.Statement, query.Arguments...)
	if err != nil {
		return nil, fmt.Errorf("inventory workspace migration tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables, rows.Err()
}
