package schema

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// SQLDatabase is the transaction/connection-neutral DDL surface used by the
// schema assembler. A Runtime migration can provide its advisory-lock-owning
// *sql.Conn while ordinary bootstrap paths can provide *sql.DB.
type SQLDatabase interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Store is the schema migration persistence contract.
type Store interface {
	SchemaDB() SQLDatabase
	Driver() string
	DatabaseSchema() string
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
	CreateIndexIfMissing(context.Context, string, string, bool, ...string) error
	EnsureRuntimeColumn(context.Context, string, string, string) error
	RuntimeTableExists(context.Context, string) (bool, error)
	MetadataIDColumnType() string
	LocalizedTextKeyColumnType() string
	RuntimeColumnDefinition(string) string
}

func sortedRuntimeSchemaTables(tables map[string][]string) []string {
	keys := make([]string, 0, len(tables))
	for key := range tables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func quotedColumnDefinitions(store Store, definitions []string) string {
	quoted := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		parts := strings.SplitN(definition, " ", 2)
		if len(parts) == 1 {
			quoted = append(quoted, store.Identifier(parts[0]))
			continue
		}
		quoted = append(quoted, store.Identifier(parts[0])+" "+store.RuntimeColumnDefinition(parts[1]))
	}
	return strings.Join(quoted, ", ")
}
