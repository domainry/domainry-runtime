package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationseed "github.com/domainry/domainry-runtime/runtime/application/seed/automation"
	businessseed "github.com/domainry/domainry-runtime/runtime/application/seed/business"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	businessseedvalidation "github.com/domainry/domainry-runtime/runtime/domain/businessseed/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type runtimeSeedSynchronizationOperations struct {
	synchronizeBusiness   func() error
	synchronizeAutomation func() error
}

func synchronizeRuntimeSeeds(ctx context.Context, store *persistence.RuntimeStore, manifest manifestmodel.ManifestSchema, businessSeedSyncEnabled bool, workforceDirectory identitysdk.Directory) error {
	if businessSeedSyncEnabled && workforceDirectory == nil {
		return fmt.Errorf("synchronize Runtime seeds: Identity SDK directory is required")
	}
	workspaceIDs := []string{principalmodel.InstallationWorkspaceID}
	for _, workspaceID := range workspaceIDs {
		workspaceContext := requestcontext.WithWorkspaceID(ctx, workspaceID)
		recordStore := recordpersistence.NewRecordStore(store)
		if err := completeRuntimeSeedSynchronization(runtimeSeedSynchronizationOperations{
			synchronizeBusiness: func() error {
				if !businessSeedSyncEnabled {
					return nil
				}
				seedManifest, deriveErr := deriveRuntimeManifestScopeOwnerSeeds(workspaceContext, workspaceID, manifest, workforceDirectory)
				if deriveErr != nil {
					return deriveErr
				}
				return businessseed.SyncManifestBusinessSeeds(workspaceContext, recordStore, changeplanpersistence.NewBusinessEvidenceStore(store), seedManifest, businessseed.ManifestBusinessSeedRowsFromManifest(seedManifest))
			},
			synchronizeAutomation: func() error {
				return automationseed.SyncExecutionSeeds(workspaceContext, automationpersistence.NewAutomationExecutionStore(store), manifest.AutomationExecutionSeeds)
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// deriveRuntimeManifestScopeOwnerSeeds applies the same canonical Workforce
// owner-department derivation used by Runtime CRUD before seed rows bypass the
// HTTP/application layer. Authored department fields are overwritten; only
// the scope_owner identity and the live active primary assignment are trusted.
func deriveRuntimeManifestScopeOwnerSeeds(ctx context.Context, workspaceID string, manifest manifestmodel.ManifestSchema, workforceDirectory identitysdk.Directory) (manifestmodel.ManifestSchema, error) {
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range manifest.Objects {
		objects[strings.TrimSpace(object.Key)] = object
	}
	derived := manifest
	derived.SeedRecords = append([]businessseedmodel.SeedRecordSchema(nil), manifest.SeedRecords...)
	deriver := recordservice.NewRecordScopeOwnerFactDerivationDomainService(recordservice.RecordScopeOwnerFactDerivationDependencies{
		WorkforceDirectory: workforceDirectory,
	})
	for index := range derived.SeedRecords {
		seed := &derived.SeedRecords[index]
		object, found := objects[strings.TrimSpace(seed.ObjectKey)]
		if !found {
			continue
		}
		data := make(map[string]any, len(seed.Data))
		for key, value := range seed.Data {
			data[key] = value
		}
		seedKey := strings.TrimSpace(fmt.Sprint(data["__seed_key"]))
		recordID := businessseedvalidation.BusinessSeedRecordID(strings.TrimSpace(seed.ObjectKey), seedKey)
		if err := deriver.Apply(ctx, workspaceID, object, data, recordID); err != nil {
			return manifestmodel.ManifestSchema{}, fmt.Errorf("derive domain seed %s/%s scope owner: %w", seed.ObjectKey, seedKey, err)
		}
		seed.Data = data
	}
	return derived, nil
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
