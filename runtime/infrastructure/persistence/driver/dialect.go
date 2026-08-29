package driver

import (
	"context"
	"database/sql"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// Dialect combines the shared SQL renderer with Runtime-owned connection and
// migration configuration. Generic rendering lives in domainry-orm.
type Dialect interface {
	Name() string
	SQLDriver() string
	DSN(config.Config) (string, error)
	Configure(context.Context, *sql.DB, string) error
	SQLDialect() ormdialect.Dialect
	SchemaMigrationSQL() string
}

// EngineProfile exposes optional SQL-engine capabilities without expanding
// the connection/migration Dialect contract implemented by test adapters.
type EngineProfile interface {
	TextKeyColumnType(int) string
	ApplyUpdateLock(*ormbuilder.SelectBuilder) *ormbuilder.SelectBuilder
}

type portableEngineProfile struct{}

func (portableEngineProfile) TextKeyColumnType(int) string { return "TEXT" }
func (portableEngineProfile) ApplyUpdateLock(builder *ormbuilder.SelectBuilder) *ormbuilder.SelectBuilder {
	return builder
}

func ProfileFor(dialect Dialect) EngineProfile {
	if profile, ok := dialect.(EngineProfile); ok {
		return profile
	}
	return portableEngineProfile{}
}
