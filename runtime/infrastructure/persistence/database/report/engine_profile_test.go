package report

import (
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func reportTestEngineProfile(name string) persistencedriver.EngineProfile {
	switch name {
	case "mysql":
		return mysql.NewEngine()
	case "postgres":
		return postgres.NewEngine()
	default:
		return sqlite.NewEngine()
	}
}
