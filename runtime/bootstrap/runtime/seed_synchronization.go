package runtime

import (
	"context"

	"github.com/domainry/domainry-foundation/requestcontext"
	automationseed "github.com/domainry/domainry-runtime/runtime/application/seed/automation"
	businessseed "github.com/domainry/domainry-runtime/runtime/application/seed/business"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type runtimeSeedSynchronizationOperations struct {
	synchronizeBusiness   func() error
	synchronizeAutomation func() error
}

func synchronizeRuntimeSeeds(ctx context.Context, store *persistence.RuntimeStore, manifest manifestmodel.ManifestSchema, businessSeedSyncEnabled bool, resolver businessseed.BaselineReferenceResolver) error {
	workspaceIDs := []string{principalmodel.InstallationWorkspaceID}
	for _, workspaceID := range workspaceIDs {
		workspaceContext := requestcontext.WithWorkspaceID(ctx, workspaceID)
		recordStore := recordpersistence.NewRecordStore(store)
		automationExecutionSeeds := append([]automationmodel.AutomationRuleExecution(nil), manifest.AutomationExecutionSeeds...)
		for index := range automationExecutionSeeds {
			automationExecutionSeeds[index].WorkspaceID = workspaceID
		}
		if err := completeRuntimeSeedSynchronization(runtimeSeedSynchronizationOperations{
			synchronizeBusiness: func() error {
				if !businessSeedSyncEnabled {
					return nil
				}
				rows, err := businessseed.BuildManifestBusinessSeedRowsWithReferenceResolver(ctx, manifest, workspaceID, resolver)
				if err != nil {
					return err
				}
				return businessseed.SyncManifestBusinessSeeds(workspaceContext, recordStore, manifest, rows)
			},
			synchronizeAutomation: func() error {
				return automationseed.SyncExecutionSeeds(workspaceContext, automationpersistence.NewAutomationExecutionStore(store), automationExecutionSeeds)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func runtimeSeedSynchronizationContext(ctx context.Context) context.Context {
	return requestcontext.WithWorkspaceID(ctx, principalmodel.InstallationWorkspaceID)
}

func completeRuntimeSeedSynchronization(operations runtimeSeedSynchronizationOperations) error {
	if err := operations.synchronizeBusiness(); err != nil {
		return err
	}
	if err := operations.synchronizeAutomation(); err != nil {
		return err
	}
	return nil
}
