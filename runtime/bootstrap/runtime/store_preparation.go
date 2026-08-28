package runtime

import (
	"context"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func prepareRuntimeStore(ctx context.Context, cfg config.Config) (*persistence.RuntimeStore, error) {
	store, err := persistence.OpenContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return completeRuntimeStoreSchemaPreparation(ctx, store)
}

func completeRuntimeStoreSchemaPreparation(ctx context.Context, store *persistence.RuntimeStore) (*persistence.RuntimeStore, error) {
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}
