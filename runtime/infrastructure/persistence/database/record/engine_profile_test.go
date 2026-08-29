package record

import (
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func testEngineProfile(name string) persistencedriver.EngineProfile {
	switch name {
	case "mysql":
		return mysql.Dialect{}.EngineProfile()
	case "postgres":
		return postgres.Dialect{}.EngineProfile()
	default:
		return sqlite.Dialect{}.EngineProfile()
	}
}
