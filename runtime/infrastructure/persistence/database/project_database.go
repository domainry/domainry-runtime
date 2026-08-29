package database

import (
	"context"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// EnsureProjectDatabase creates the configured project database before the
// Runtime opens its single process-owned pool.
func EnsureProjectDatabase(ctx context.Context, cfg config.Config) error {
	dialect, err := dialectFor(cfg.DatabaseDriver)
	if err != nil {
		return err
	}
	return persistencedriver.ProfileFor(dialect).EnsureProjectDatabase(ctx, cfg)
}
