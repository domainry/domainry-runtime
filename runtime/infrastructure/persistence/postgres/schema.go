package postgres

import (
	"strings"

	ormdriver "github.com/domainry/domainry-orm/driver"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormpostgres.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return true }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
