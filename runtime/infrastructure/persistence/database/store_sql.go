package database

import (
	"context"
	"fmt"
	"strings"
)

func (s *RuntimeStore) insertSystemRowContext(ctx context.Context, table string, columns []string, values []any) error {
	query := "INSERT INTO " + s.tableIdentifier(table) + " (" + strings.Join(quotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
	if _, err := s.db.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert %s: %w", table, err)
	}
	return nil
}

func (s *RuntimeStore) updateSystemRowContext(ctx context.Context, table, id string, columns []string, values []any) error {
	assignments := make([]string, 0, len(columns))
	for index, column := range columns {
		assignments = append(assignments, s.identifier(column)+" = "+s.placeholder(index+1))
	}
	values = append(values, id)
	query := "UPDATE " + s.tableIdentifier(table) + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.identifier("id") + " = " + s.placeholder(len(values))
	if _, err := s.db.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("update %s: %w", table, err)
	}
	return nil
}

func quotedColumns(s *RuntimeStore, columns []string) []string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, s.identifier(column))
	}
	return quoted
}

func placeholders(s *RuntimeStore, count int) []string {
	values := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		values = append(values, s.placeholder(idx+1))
	}
	return values
}

func (s *RuntimeStore) identifier(value string) string {
	return s.dialect.SQLDialect().Identifier(value)
}

func (s *RuntimeStore) tableIdentifier(value string) string {
	return s.dialect.SQLDialect().Table(strings.TrimSpace(s.databaseSchema), value)
}

// Identifier exposes the database-specific quoting policy to domain-owned
// repository adapters without exposing Store internals.
func (s *RuntimeStore) Identifier(value string) string {
	return s.identifier(value)
}

// TableIdentifier schema-qualifies PostgreSQL relations while leaving column,
// constraint, and index identifiers unqualified.
func (s *RuntimeStore) TableIdentifier(value string) string {
	return s.tableIdentifier(value)
}

func (s *RuntimeStore) placeholder(position int) string {
	return s.dialect.SQLDialect().Placeholder(position)
}

// Placeholder exposes the database-specific placeholder syntax to
// domain-owned repository adapters.
func (s *RuntimeStore) Placeholder(position int) string {
	return s.placeholder(position)
}
