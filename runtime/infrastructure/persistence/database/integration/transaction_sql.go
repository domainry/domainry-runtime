package integration

import (
	"database/sql"
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// recordMutationTxOptions defines the isolation required by integration mutations.
func recordMutationTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}
