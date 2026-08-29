package database

import (
	"context"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
)

func (s *RuntimeStore) sqlBase() *base.SQLStore {
	if s.SQLStore == nil {
		s.SQLStore = base.NewSQLStore(s.db, s.dialect.SQLDialect(), s.databaseSchema)
	}
	return s.SQLStore
}

func quotedColumns(s *RuntimeStore, columns []string) []string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = s.sqlBase().SQLRenderer.Identifier(column)
	}
	return quoted
}

func placeholders(s *RuntimeStore, count int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = s.sqlBase().SQLRenderer.Placeholder(index + 1)
	}
	return values
}

func (s *RuntimeStore) insertSystemRowContext(ctx context.Context, table string, columns []string, values []any) error {
	query, args, err := ormbuilder.NewInsertBuilder(s.sqlBase().SQLRenderer, table).Columns(columns...).Values(values...).Build()
	if err != nil {
		return fmt.Errorf("build insert %s: %w", table, err)
	}
	if _, err := s.sqlBase().DB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert %s: %w", table, err)
	}
	return nil
}

func (s *RuntimeStore) updateSystemRowContext(ctx context.Context, table, id string, columns []string, values []any) error {
	builder := ormbuilder.NewUpdateBuilder(s.sqlBase().SQLRenderer, table)
	for index, column := range columns {
		builder.Set(column, values[index])
	}
	query, args, err := builder.Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return fmt.Errorf("build update %s: %w", table, err)
	}
	if _, err := s.sqlBase().DB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update %s: %w", table, err)
	}
	return nil
}

func (s *RuntimeStore) identifier(value string) string {
	return s.sqlBase().SQLRenderer.Identifier(value)
}

func (s *RuntimeStore) tableIdentifier(value string) string {
	return s.sqlBase().SQLRenderer.Table(value)
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
	return s.sqlBase().SQLRenderer.Placeholder(position)
}

// Placeholder exposes the database-specific placeholder syntax to
// domain-owned repository adapters.
func (s *RuntimeStore) Placeholder(position int) string {
	return s.placeholder(position)
}
