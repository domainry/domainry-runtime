package sqlite

import (
	"strings"

	ormdriver "github.com/domainry/domainry-orm/driver"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormsqlite.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return false }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
