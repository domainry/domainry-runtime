// Package lifecyclesdkfixture opens the real Lifecycle module through its SDK
// boundary for Runtime integration tests.
package lifecyclesdkfixture

import (
	"context"
	"fmt"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclemoduleimpl "github.com/domainry/domainry-lifecycle/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func Open(ctx context.Context, store *database.RuntimeStore, runtimeID string) (lifecyclesdk.Binding, error) {
	if store == nil {
		return nil, fmt.Errorf("Lifecycle test store is required")
	}
	binding, err := lifecyclemoduleimpl.NewFactory().OpenModule(ctx, lifecyclesdk.ApplicationRef{RuntimeID: runtimeID}, lifecyclemodule.NewHost(store))
	if err != nil {
		return nil, err
	}
	if err := binding.Descriptor().Validate(); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return binding, nil
}
