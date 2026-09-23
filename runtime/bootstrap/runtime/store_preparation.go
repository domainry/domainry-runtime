package runtime

import (
	"context"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func prepareRuntimeStore(ctx context.Context, cfg config.Config, selected ...persistence.RuntimeSchemaCapabilities) (*persistence.RuntimeStore, error) {
	if err := persistence.EnsureProjectDatabase(ctx, cfg); err != nil {
		return nil, err
	}
	store, err := persistence.OpenContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return completeRuntimeStoreSchemaPreparation(ctx, store, selected...)
}

// PrepareProjectDatabase creates the project-owned pool and completes Runtime
// schema preparation before in-process modules borrow that pool.
func PrepareProjectDatabase(ctx context.Context, cfg config.Config, capabilities persistence.RuntimeSchemaCapabilities) (*persistence.RuntimeStore, error) {
	return prepareRuntimeStore(ctx, cfg, capabilities)
}

func completeRuntimeStoreSchemaPreparation(ctx context.Context, store *persistence.RuntimeStore, selected ...persistence.RuntimeSchemaCapabilities) (*persistence.RuntimeStore, error) {
	capabilities := persistence.FullRuntimeSchemaCapabilities()
	if len(selected) != 0 {
		capabilities = selected[0]
	}
	if err := store.EnsureRuntimeSchemaFor(ctx, capabilities); err != nil {
		return nil, err
	}
	return store, nil
}
