package database

import (
	"context"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// EnsureProjectDatabase creates the configured project database before the
// Runtime opens its single process-owned pool.
func EnsureProjectDatabase(ctx context.Context, cfg config.Config) error {
	engine, err := engineFor(cfg.DatabaseDriver)
	if err != nil {
		return err
	}
	return engine.EnsureProjectDatabase(ctx, cfg)
}
