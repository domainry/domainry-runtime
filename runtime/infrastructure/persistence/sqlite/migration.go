package sqlite

import (
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func (engineProfile) MigrationDatabasePath(cfg config.Config) string {
	if value := strings.TrimSpace(cfg.DBPath); value != "" {
		return value
	}
	return strings.TrimSpace(cfg.DatabaseDSN)
}
