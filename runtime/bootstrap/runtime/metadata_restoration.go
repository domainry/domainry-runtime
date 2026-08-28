package runtime

import (
	"context"

	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestrepository "github.com/domainry/domainry-runtime/runtime/domain/manifest/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

type restoredRuntimeMetadata struct {
	manifest             manifestmodel.ManifestSchema
	metadataStore        metadatapersistence.MetadataStore
	deliveryMetricsStore notificationpersistence.DeliveryMetricsStore
}

func restoreRuntimeMetadata(ctx context.Context, store *persistence.RuntimeStore, seedManifest manifestmodel.ManifestSchema) (restoredRuntimeMetadata, error) {
	deliveryMetricsStore := notificationpersistence.NewDeliveryMetricsStore(store)
	templateCatalog := notificationpersistence.NewManifestTemplateCatalog(store)
	metadataStore := metadatapersistence.NewMetadataStore(store)
	manifest, err := restoreRuntimeManifest(ctx, templateCatalog, metadataStore, seedManifest)
	if err != nil {
		return restoredRuntimeMetadata{}, err
	}
	return restoredRuntimeMetadata{manifest: manifest, metadataStore: metadataStore, deliveryMetricsStore: deliveryMetricsStore}, nil
}

func restoreRuntimeManifest(ctx context.Context, notifications metadataapplication.InstalledNotificationTemplateCatalog, metadata manifestrepository.ManifestRuntimeMetadataRepository, seedManifest manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "restore Runtime metadata")
	return metadataapplication.NewMetadataRuntimeRestorationApplicationService(notifications, metadata).Restore(ctx, seedManifest, scope)
}
