package schema

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormschema "github.com/domainry/domainry-orm/schema"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

// SQLDatabase is the transaction/connection-neutral DDL contract used by the
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
	RuntimeTableExists(context.Context, string) (bool, error)
	ApplicationSchemaIDColumnType() string
	LocalizedTextKeyColumnType() string
	RuntimeColumnDefinition(string) string
	RuntimeProfile() persistencedriver.EngineProfile
	RuntimeRenderer() ormdialect.Renderer
}

func sortedRuntimeSchemaTables(tables map[string][]string) []string {
	keys := make([]string, 0, len(tables))
	for key := range tables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func quotedColumnDefinitions(store Store, definitions []string, identities ...string) string {
	names := []string{}
	byName := make(map[string]string, len(definitions))
	constraints := []string{}
	for _, definition := range definitions {
		if _, ok := quotedPrimaryKeyConstraint(store, definition); ok {
			constraints = append(constraints, definition)
			continue
		}
		name := strings.Fields(definition)[0]
		names, byName[name] = append(names, name), definition
	}
	ordered := make([]string, 0, len(definitions))
	for _, name := range ormschema.OrderColumnNames(names, identities...) {
		ordered = append(ordered, byName[name])
	}
	definitions = append(ordered, constraints...)
	quoted := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if constraint, ok := quotedPrimaryKeyConstraint(store, definition); ok {
			quoted = append(quoted, constraint)
			continue
		}
		parts := strings.SplitN(definition, " ", 2)
		if len(parts) == 1 {
			quoted = append(quoted, store.Identifier(parts[0]))
			continue
		}
		quoted = append(quoted, store.Identifier(parts[0])+" "+store.RuntimeColumnDefinition(parts[1]))
	}
	return strings.Join(quoted, ", ")
}

func runtimeEntityColumns(table string) []string {
	for _, ownership := range RuntimeSchemaOwnership() {
		if ownership.Name == table && ownership.EntityIDColumn != "" {
			return []string{ownership.EntityIDColumn}
		}
	}
	return nil
}

func quotedPrimaryKeyConstraint(store Store, definition string) (string, bool) {
	definition = strings.TrimSpace(definition)
	const prefix = "PRIMARY KEY ("
	if !strings.HasPrefix(strings.ToUpper(definition), prefix) || !strings.HasSuffix(definition, ")") {
		return "", false
	}
	columns := strings.Split(definition[len(prefix):len(definition)-1], ",")
	for index, column := range columns {
		columns[index] = store.Identifier(strings.TrimSpace(column))
	}
	return "PRIMARY KEY (" + strings.Join(columns, ", ") + ")", true
}
