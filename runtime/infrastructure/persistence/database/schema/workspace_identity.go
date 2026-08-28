package schema

import (
	"context"
	"fmt"
	"strings"
)

// prepareWorkspaceScopedIdentities removes installation-global primary keys from
// workspace-owned tables and returns the former key column for a composite
// (workspace_id, key) unique constraint. Runtime repositories must always use
// both columns when addressing these rows.
func prepareWorkspaceScopedIdentities(tables map[string][]string) map[string]string {
	identities := make(map[string]string)
	for table, definitions := range tables {
		if !hasWorkspaceColumn(definitions) {
			continue
		}
		for index, definition := range definitions {
			if !strings.Contains(definition, " PRIMARY KEY") {
				continue
			}
			column := strings.Fields(definition)[0]
			definitions[index] = strings.Replace(definition, " PRIMARY KEY", "", 1)
			if !strings.Contains(definitions[index], " NOT NULL") {
				definitions[index] += " NOT NULL"
			}
			identities[table] = column
		}
		tables[table] = definitions
	}
	return identities
}

func ensureWorkspaceScopedIdentities(ctx context.Context, s Store, identities map[string]string) error {
	for _, table := range sortedRuntimeSchemaTablesByKey(identities) {
		key := identities[table]
		columns := []string{"workspace_id"}
		if key != "workspace_id" {
			columns = append(columns, key)
		}
		if err := s.CreateIndexIfMissing(ctx, table, workspaceIdentityIndexName(table), true, columns...); err != nil {
			return fmt.Errorf("create workspace identity for %s: %w", table, err)
		}
	}
	return nil
}

func workspaceIdentityIndexName(table string) string {
	return "uniq_" + strings.TrimPrefix(table, "_") + "_workspace_identity"
}

func hasWorkspaceColumn(definitions []string) bool {
	for _, definition := range definitions {
		if strings.Fields(definition)[0] == "workspace_id" {
			return true
		}
	}
	return false
}

func sortedRuntimeSchemaTablesByKey(values map[string]string) []string {
	tables := make(map[string][]string, len(values))
	for table := range values {
		tables[table] = nil
	}
	return sortedRuntimeSchemaTables(tables)
}
