package database

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-notification-sdk/modulehost"
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

func (s *RuntimeStore) proveModuleMigrationBaseline(ctx context.Context, baseline *modulehost.SchemaBaseline) (bool, error) {
	if baseline == nil || len(baseline.Tables) == 0 {
		return false, nil
	}
	found := 0
	for _, expected := range baseline.Tables {
		if !moduleMigrationIdentityPattern.MatchString(expected.Name) || len(expected.Columns) == 0 {
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

func compareModuleSchemaTable(expected modulehost.SchemaTable, actual moduleSchemaTable) error {
	if len(actual.columns) != len(expected.Columns) {
		return fmt.Errorf("columns=%d want=%d", len(actual.columns), len(expected.Columns))
	}
	for position, want := range expected.Columns {
		if !moduleMigrationIdentityPattern.MatchString(want.Name) {
			return fmt.Errorf("invalid expected column %q", want.Name)
		}
		got := actual.columns[position]
		if got.name != want.Name || normalizeModuleColumnType(got.physical) != normalizeModuleColumnType(want.Type) || got.nullable != want.Nullable || got.primaryKey != want.PrimaryKey {
			return fmt.Errorf("column[%d]=%s %s nullable=%t primary=%t, want %s %s nullable=%t primary=%t", position, got.name, got.physical, got.nullable, got.primaryKey, want.Name, want.Type, want.Nullable, want.PrimaryKey)
		}
	}
	if len(actual.indexes) != len(expected.Indexes) {
		return fmt.Errorf("explicit indexes=%d want=%d", len(actual.indexes), len(expected.Indexes))
	}
	sort.Slice(actual.indexes, func(i, j int) bool { return actual.indexes[i].name < actual.indexes[j].name })
	wanted := append([]modulehost.SchemaIndex(nil), expected.Indexes...)
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].Name < wanted[j].Name })
	for position, want := range wanted {
		got := actual.indexes[position]
		if got.name != want.Name || got.unique != want.Unique || !equalModuleColumns(got.columns, want.Columns) {
			return fmt.Errorf("index[%d]=%s unique=%t columns=%v, want %s unique=%t columns=%v", position, got.name, got.unique, got.columns, want.Name, want.Unique, want.Columns)
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
