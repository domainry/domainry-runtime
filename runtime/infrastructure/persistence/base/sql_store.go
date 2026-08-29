// Package base owns the database/sql handle and portable SQL rendering shared
// by Runtime persistence adapters. It does not select a database engine and it
// contains no business persistence behavior.
package base

import (
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

// SQLStore is the engine-neutral persistence foundation used by Runtime
// repositories. Engine packages construct it with their selected renderer.
type SQLStore struct {
	DB             *sql.DB
	SQLRenderer    ormdialect.Renderer
	DatabaseSchema string
}

func NewSQLStore(database *sql.DB, dialect ormdialect.Dialect, schema string) *SQLStore {
	schema = strings.TrimSpace(schema)
	return &SQLStore{DB: database, SQLRenderer: dialect.WithSchema(schema), DatabaseSchema: schema}
}
