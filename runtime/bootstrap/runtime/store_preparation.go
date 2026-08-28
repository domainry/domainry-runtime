package runtime

import (
	"context"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func prepareRuntimeStore(ctx context.Context, cfg config.Config) (*persistence.RuntimeStore, error) {
	if err := persistence.EnsureProjectDatabase(ctx, cfg); err != nil {
		return nil, err
	}
	store, err := persistence.OpenContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return completeRuntimeStoreSchemaPreparation(ctx, store)
}

// PrepareProjectDatabase creates the project-owned pool and completes Runtime
// schema preparation before in-process modules borrow that pool.
func PrepareProjectDatabase(ctx context.Context, cfg config.Config) (*persistence.RuntimeStore, error) {
	return prepareRuntimeStore(ctx, cfg)
}

func completeRuntimeStoreSchemaPreparation(ctx context.Context, store *persistence.RuntimeStore) (*persistence.RuntimeStore, error) {
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}
