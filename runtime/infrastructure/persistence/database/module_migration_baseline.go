package database

import (
	"context"
	"fmt"
	"strings"

	ormmigration "github.com/domainry/domainry-orm/migration"
)

type moduleSchemaColumn struct {
	name       string
	physical   string
	nullable   bool
	primaryKey bool
}

type moduleSchemaIndex struct {
	name    string
	unique  bool
	columns []string
}

type moduleSchemaTable struct {
	columns []moduleSchemaColumn
	indexes []moduleSchemaIndex
}

func (s *RuntimeStore) proveModuleMigrationBaseline(ctx context.Context, baseline *ormmigration.Baseline) (bool, error) {
	if baseline == nil || len(baseline.Tables) == 0 {
		return false, nil
	}
	found := 0
	for _, expected := range baseline.Tables {
		if !moduleSchemaIdentityPattern.MatchString(expected.Name) || len(expected.Columns) == 0 {
			return false, fmt.Errorf("invalid baseline table %q", expected.Name)
		}
		actual, exists, err := s.inspectModuleSchemaTable(ctx, expected.Name)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		found++
		if err := compareModuleSchemaTable(expected, actual); err != nil {
			return false, fmt.Errorf("baseline schema mismatch for %s: %w", expected.Name, err)
		}
	}
	if found == 0 {
		return false, nil
	}
	if found != len(baseline.Tables) {
		return false, fmt.Errorf("partial baseline: found %d of %d owned tables", found, len(baseline.Tables))
	}
	return true, nil
}

func compareModuleSchemaTable(expected ormmigration.Table, actual moduleSchemaTable) error {
	return compareModuleSchemaTableIndexes(expected, actual, true)
}

func compareModuleSchemaTableSubset(expected ormmigration.Table, actual moduleSchemaTable) error {
	return compareModuleSchemaTableIndexes(expected, actual, false)
}

func compareModuleSchemaTableIndexes(expected ormmigration.Table, actual moduleSchemaTable, exactIndexes bool) error {
	if len(actual.columns) != len(expected.Columns) {
		return fmt.Errorf("columns=%d want=%d", len(actual.columns), len(expected.Columns))
	}
	columns := make(map[string]moduleSchemaColumn, len(actual.columns))
	for _, column := range actual.columns {
		columns[column.name] = column
	}
	for _, want := range expected.Columns {
		if !moduleSchemaIdentityPattern.MatchString(want.Name) {
			return fmt.Errorf("invalid expected column %q", want.Name)
		}
		got, found := columns[want.Name]
		if !found {
			return fmt.Errorf("column %s is missing", want.Name)
		}
		if got.name != want.Name || normalizeModuleColumnType(got.physical) != normalizeModuleColumnType(want.Type) || got.nullable != want.Nullable || got.primaryKey != want.PrimaryKey {
			return fmt.Errorf("column %s=%s nullable=%t primary=%t, want %s nullable=%t primary=%t", got.name, got.physical, got.nullable, got.primaryKey, want.Type, want.Nullable, want.PrimaryKey)
		}
	}
	if exactIndexes && len(actual.indexes) != len(expected.Indexes) {
		return fmt.Errorf("explicit indexes=%d want=%d", len(actual.indexes), len(expected.Indexes))
	}
	indexes := make(map[string]moduleSchemaIndex, len(actual.indexes))
	for _, index := range actual.indexes {
		indexes[index.name] = index
	}
	for _, want := range expected.Indexes {
		got, exists := indexes[want.Name]
		if !exists {
			return fmt.Errorf("index %s is missing", want.Name)
		}
		if got.unique != want.Unique || !equalModuleColumns(got.columns, want.Columns) {
			return fmt.Errorf("index %s unique=%t columns=%v, want unique=%t columns=%v", got.name, got.unique, got.columns, want.Unique, want.Columns)
		}
	}
	return nil
}

func equalModuleColumns(left, right []string) bool {
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

func normalizeModuleColumnType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "INT", "INT(11)":
		return "INTEGER"
	case "TINYINT(1)", "BOOL":
		return "BOOLEAN"
	case "CHARACTER VARYING(191)":
		return "VARCHAR(191)"
	default:
		return value
	}
}

func (s *RuntimeStore) inspectModuleSchemaTable(ctx context.Context, table string) (moduleSchemaTable, bool, error) {
	inspected, exists, err := s.RuntimeProfile().InspectModuleSchemaTable(ctx, s.schemaDatabase(), s.SQLRenderer, s.DatabaseSchema(), table)
	if err != nil || !exists {
		return moduleSchemaTable{}, exists, err
	}
	result := moduleSchemaTable{columns: make([]moduleSchemaColumn, len(inspected.Columns)), indexes: make([]moduleSchemaIndex, len(inspected.Indexes))}
	for index, column := range inspected.Columns {
		result.columns[index] = moduleSchemaColumn{name: column.Name, physical: column.Physical, nullable: column.Nullable, primaryKey: column.PrimaryKey}
	}
	for index, item := range inspected.Indexes {
		result.indexes[index] = moduleSchemaIndex{name: item.Name, unique: item.Unique, columns: append([]string(nil), item.Columns...)}
	}
	return result, true, nil
}
