package mysql

import "github.com/domainry/domainry-runtime/runtime/platform/config"

func (engineProfile) MigrationDatabasePath(config.Config) string { return "" }
