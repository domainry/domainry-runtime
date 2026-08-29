package database

import (
	"context"
	"fmt"

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
	ensurer, ok := dialect.(persistencedriver.ProjectDatabaseEnsurer)
	if !ok {
		return fmt.Errorf("database driver %q does not support project database initialization", dialect.Name())
	}
	return ensurer.EnsureProjectDatabase(ctx, cfg)
}
