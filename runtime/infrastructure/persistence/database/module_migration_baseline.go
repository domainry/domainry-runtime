package database

import (
	"context"
	"database/sql"
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
	switch strings.ToLower(strings.TrimSpace(s.Driver())) {
	case "sqlite":
		return s.inspectSQLiteModuleSchemaTable(ctx, table)
	case "postgres", "postgresql", "pgx":
		return s.inspectPostgresModuleSchemaTable(ctx, table)
	case "mysql":
		return s.inspectMySQLModuleSchemaTable(ctx, table)
	default:
		return moduleSchemaTable{}, false, fmt.Errorf("unsupported module baseline driver %q", s.Driver())
	}
}

func (s *RuntimeStore) inspectSQLiteModuleSchemaTable(ctx context.Context, table string) (moduleSchemaTable, bool, error) {
	rows, err := s.schemaDatabase().QueryContext(ctx, "PRAGMA table_info("+s.identifier(table)+")")
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	result := moduleSchemaTable{}
	for rows.Next() {
		var ordinal, notNull, primary int
		var name, physical string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &physical, &notNull, &defaultValue, &primary); err != nil {
			_ = rows.Close()
			return moduleSchemaTable{}, false, err
		}
		result.columns = append(result.columns, moduleSchemaColumn{name: name, physical: physical, nullable: notNull == 0, primaryKey: primary > 0})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return moduleSchemaTable{}, false, err
	}
	_ = rows.Close()
	if len(result.columns) == 0 {
		return moduleSchemaTable{}, false, nil
	}
	// SQLite's catalog reports a non-INTEGER PRIMARY KEY as nullable even
	// though it is the table identity. Legacy Runtime DDL used that spelling;
	// adopt it only when the existing data proves the key contains no NULL.
	for index := range result.columns {
		if !result.columns[index].primaryKey || !result.columns[index].nullable {
			continue
		}
		var nulls int
		query := "SELECT COUNT(*) FROM " + s.tableIdentifier(table) + " WHERE " + s.identifier(result.columns[index].name) + " IS NULL"
		if err := s.schemaDatabase().QueryRowContext(ctx, query).Scan(&nulls); err != nil {
			return moduleSchemaTable{}, false, err
		}
		if nulls != 0 {
			return moduleSchemaTable{}, false, fmt.Errorf("primary key %s.%s contains %d NULL values", table, result.columns[index].name, nulls)
		}
		result.columns[index].nullable = false
	}
	indexRows, err := s.schemaDatabase().QueryContext(ctx, "PRAGMA index_list("+s.identifier(table)+")")
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	indexes := []moduleSchemaIndex{}
	for indexRows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := indexRows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = indexRows.Close()
			return moduleSchemaTable{}, false, err
		}
		if origin == "pk" || strings.HasPrefix(name, "sqlite_autoindex_") {
			continue
		}
		indexes = append(indexes, moduleSchemaIndex{name: name, unique: unique == 1})
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return moduleSchemaTable{}, false, err
	}
	_ = indexRows.Close()
	for _, index := range indexes {
		columns, err := s.sqliteModuleIndexColumns(ctx, index.name)
		if err != nil {
			return moduleSchemaTable{}, false, err
		}
		index.columns = columns
		result.indexes = append(result.indexes, index)
	}
	return result, true, nil
}

func (s *RuntimeStore) sqliteModuleIndexColumns(ctx context.Context, index string) ([]string, error) {
	rows, err := s.schemaDatabase().QueryContext(ctx, "PRAGMA index_info("+s.identifier(index)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var rank, columnID int
		var name string
		if err := rows.Scan(&rank, &columnID, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func (s *RuntimeStore) inspectPostgresModuleSchemaTable(ctx context.Context, table string) (moduleSchemaTable, bool, error) {
	schema := strings.TrimSpace(s.DatabaseSchema())
	if schema == "" {
		schema = "public"
	}
	rows, err := s.schemaDatabase().QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	result := moduleSchemaTable{}
	for rows.Next() {
		var column moduleSchemaColumn
		var nullable string
		if err := rows.Scan(&column.name, &column.physical, &nullable); err != nil {
			_ = rows.Close()
			return moduleSchemaTable{}, false, err
		}
		column.nullable = nullable == "YES"
		result.columns = append(result.columns, column)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return moduleSchemaTable{}, false, err
	}
	_ = rows.Close()
	if len(result.columns) == 0 {
		return moduleSchemaTable{}, false, nil
	}
	return s.completePostgresModuleSchemaTable(ctx, schema, table, result)
}

func (s *RuntimeStore) completePostgresModuleSchemaTable(ctx context.Context, schema, table string, result moduleSchemaTable) (moduleSchemaTable, bool, error) {
	primaryRows, err := s.schemaDatabase().QueryContext(ctx, `SELECT attribute.attname FROM pg_index idx JOIN pg_class relation ON relation.oid = idx.indrelid JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace JOIN LATERAL unnest(idx.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON true JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = key_column.attnum WHERE namespace.nspname = $1 AND relation.relname = $2 AND idx.indisprimary ORDER BY key_column.ordinality`, schema, table)
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	primary := map[string]bool{}
	for primaryRows.Next() {
		var name string
		if err := primaryRows.Scan(&name); err != nil {
			_ = primaryRows.Close()
			return moduleSchemaTable{}, false, err
		}
		primary[name] = true
	}
	if err := primaryRows.Err(); err != nil {
		_ = primaryRows.Close()
		return moduleSchemaTable{}, false, err
	}
	_ = primaryRows.Close()
	for index := range result.columns {
		result.columns[index].primaryKey = primary[result.columns[index].name]
	}
	indexRows, err := s.schemaDatabase().QueryContext(ctx, `SELECT index_class.relname, idx.indisunique, array_to_string(array_agg(attribute.attname ORDER BY key_column.ordinality), ',') FROM pg_index idx JOIN pg_class relation ON relation.oid = idx.indrelid JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace JOIN pg_class index_class ON index_class.oid = idx.indexrelid JOIN LATERAL unnest(idx.indkey) WITH ORDINALITY AS key_column(attnum, ordinality) ON key_column.attnum > 0 JOIN pg_attribute attribute ON attribute.attrelid = relation.oid AND attribute.attnum = key_column.attnum WHERE namespace.nspname = $1 AND relation.relname = $2 AND NOT idx.indisprimary GROUP BY index_class.relname, idx.indisunique ORDER BY index_class.relname`, schema, table)
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var index moduleSchemaIndex
		var columns string
		if err := indexRows.Scan(&index.name, &index.unique, &columns); err != nil {
			return moduleSchemaTable{}, false, err
		}
		index.columns = strings.Split(columns, ",")
		result.indexes = append(result.indexes, index)
	}
	return result, true, indexRows.Err()
}

func (s *RuntimeStore) inspectMySQLModuleSchemaTable(ctx context.Context, table string) (moduleSchemaTable, bool, error) {
	schema := strings.TrimSpace(s.DatabaseSchema())
	if schema == "" {
		if err := s.schemaDatabase().QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
			return moduleSchemaTable{}, false, err
		}
	}
	rows, err := s.schemaDatabase().QueryContext(ctx, `SELECT column_name, column_type, is_nullable, column_key FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	result := moduleSchemaTable{}
	for rows.Next() {
		var column moduleSchemaColumn
		var nullable, key string
		if err := rows.Scan(&column.name, &column.physical, &nullable, &key); err != nil {
			_ = rows.Close()
			return moduleSchemaTable{}, false, err
		}
		column.nullable, column.primaryKey = nullable == "YES", key == "PRI"
		result.columns = append(result.columns, column)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return moduleSchemaTable{}, false, err
	}
	_ = rows.Close()
	if len(result.columns) == 0 {
		return moduleSchemaTable{}, false, nil
	}
	indexRows, err := s.schemaDatabase().QueryContext(ctx, `SELECT index_name, non_unique, column_name FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? AND index_name <> 'PRIMARY' ORDER BY index_name, seq_in_index`, schema, table)
	if err != nil {
		return moduleSchemaTable{}, false, err
	}
	defer indexRows.Close()
	byName := map[string]*moduleSchemaIndex{}
	order := []string{}
	for indexRows.Next() {
		var name, column string
		var nonUnique int
		if err := indexRows.Scan(&name, &nonUnique, &column); err != nil {
			return moduleSchemaTable{}, false, err
		}
		index := byName[name]
		if index == nil {
			index = &moduleSchemaIndex{name: name, unique: nonUnique == 0}
			byName[name] = index
			order = append(order, name)
		}
		index.columns = append(index.columns, column)
	}
	if err := indexRows.Err(); err != nil {
		return moduleSchemaTable{}, false, err
	}
	for _, name := range order {
		result.indexes = append(result.indexes, *byName[name])
	}
	return result, true, nil
}
