package mysql

import (
	"strings"

	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormmysql.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool { return true }
func (engineProfile) ColumnDefinition(definition string) string {
	definition = strings.TrimSpace(definition)
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '[]'", "TEXT NOT NULL DEFAULT ('[]')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '{}'", "TEXT NOT NULL DEFAULT ('{}')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ('')")
	return definition
}
