package schema

import (
	"strings"
)

// prepareWorkspaceScopedIdentities replaces installation-global inline primary
// keys with the physical composite primary key used by workspace-owned tables.
// Runtime repositories must always use both columns when addressing these rows.
func prepareWorkspaceScopedIdentities(tables map[string][]string) {
	for table, definitions := range tables {
		if !hasWorkspaceColumn(definitions) {
			continue
		}
		primaryColumn := ""
		for index, definition := range definitions {
			if !strings.Contains(definition, " PRIMARY KEY") {
				continue
			}
			primaryColumn = strings.Fields(definition)[0]
			definitions[index] = strings.Replace(definition, " PRIMARY KEY", "", 1)
			if !strings.Contains(definitions[index], " NOT NULL") {
				definitions[index] += " NOT NULL"
			}
		}
		if primaryColumn != "" {
			columns := "workspace_id, " + primaryColumn
			if primaryColumn == "workspace_id" {
				columns = "workspace_id"
			}
			definitions = append(definitions, "PRIMARY KEY ("+columns+")")
		}
		tables[table] = definitions
	}
}

func hasWorkspaceColumn(definitions []string) bool {
	for _, definition := range definitions {
		if strings.Fields(definition)[0] == "workspace_id" {
			return true
		}
	}
	return false
}
