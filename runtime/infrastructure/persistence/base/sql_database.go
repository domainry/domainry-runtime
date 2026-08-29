// Package base owns the database/sql handle and portable SQL rendering shared
// by Runtime persistence adapters. It does not select a database engine and it
// contains no business persistence behavior.
package base

import (
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

// SQLDatabase is the engine-neutral database foundation used by Runtime
// persistence owners. Engine packages construct it with their selected
// renderer and Profile; it is not a business Repository or Store.
type SQLDatabase struct {
	DB             *sql.DB
	SQLRenderer    ormdialect.Renderer
	DatabaseSchema string
	Engine         ormdriver.Profile
	RuntimeEngine  driver.EngineProfile
}

func NewSQLDatabase(database *sql.DB, engine driver.Dialect, schema string) *SQLDatabase {
	schema = strings.TrimSpace(schema)
	profile := driver.ProfileFor(engine)
	return &SQLDatabase{DB: database, SQLRenderer: engine.SQLDialect().WithSchema(schema), DatabaseSchema: schema, Engine: profile, RuntimeEngine: profile}
}

// IsTransientError applies the engine Profile's stable error taxonomy. Owners
// use this retry decision without inspecting driver names or error strings.
func (d *SQLDatabase) IsTransientError(err error) bool {
	if d == nil || d.Engine == nil || err == nil {
		return false
	}
	switch d.Engine.ClassifyError(err) {
	case ormdriver.ErrorSerialization, ormdriver.ErrorDeadlock, ormdriver.ErrorUnavailable, ormdriver.ErrorTimeout:
		return true
	default:
		return false
	}
}

// IsCoordinationRetryableError additionally treats uniqueness races as
// retryable. It is reserved for lease/cohort acquisition loops where another
// writer winning the same identity is an expected coordination outcome.
func (d *SQLDatabase) IsCoordinationRetryableError(err error) bool {
	if d == nil || d.Engine == nil || err == nil {
		return false
	}
	return d.IsTransientError(err) || d.Engine.ClassifyError(err) == ormdriver.ErrorConflict
}
