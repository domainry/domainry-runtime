package runtime

import (
	"context"
	"fmt"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestrepository "github.com/domainry/domainry-runtime/runtime/domain/manifest/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

type restoredRuntimeMetadata struct {
	manifest             manifestmodel.ManifestSchema
	metadataStore        appschemapersistence.ApplicationSchemaStore
	deliveryMetricsStore notificationpersistence.DeliveryMetricsStore
}

func restoreRuntimeMetadata(ctx context.Context, store *persistence.RuntimeStore, seedManifest manifestmodel.ManifestSchema) (restoredRuntimeMetadata, error) {
	deliveryMetricsStore := notificationpersistence.NewDeliveryMetricsStore(store)
	metadataStore := appschemapersistence.NewApplicationSchemaStore(store)
	manifest, err := restoreRuntimeManifest(ctx, &installedNotificationTemplateCatalog{}, metadataStore, seedManifest)
	if err != nil {
		return restoredRuntimeMetadata{}, err
	}
	return restoredRuntimeMetadata{manifest: manifest, metadataStore: metadataStore, deliveryMetricsStore: deliveryMetricsStore}, nil
}

type installedNotificationTemplateCatalog struct {
	values []notificationmodel.NotificationTemplate
}

func (c *installedNotificationTemplateCatalog) SyncPublished(_ context.Context, _ principalmodel.SystemScope, values []notificationmodel.NotificationTemplate) error {
	c.values = append([]notificationmodel.NotificationTemplate(nil), values...)
	return nil
}

func (c *installedNotificationTemplateCatalog) List(context.Context, principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	result := make([]notificationmodel.NotificationTemplateRecord, len(c.values))
	for index := range c.values {
		value := c.values[index]
		result[index] = notificationmodel.NotificationTemplateRecord{Key: value.Key, Published: &value, PublishedVersion: value.Version, Status: value.Status}
	}
	return result, nil
}

type sdkNotificationTemplateCatalog struct {
	system notificationsdk.SystemTemplates
}

func (c sdkNotificationTemplateCatalog) SyncPublished(ctx context.Context, _ principalmodel.SystemScope, values []notificationmodel.NotificationTemplate) error {
	if c.system == nil {
		return fmt.Errorf("Notification system template port is unavailable")
	}
	converted, err := notificationSDKConvert[[]sdkcontract.NotificationTemplate](values)
	if err != nil {
		return err
	}
	return c.system.SyncPublished(ctx, converted)
}

func (c sdkNotificationTemplateCatalog) List(ctx context.Context, _ principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	if c.system == nil {
		return nil, fmt.Errorf("Notification system template port is unavailable")
	}
	values, err := c.system.ListPublished(ctx)
	if err != nil {
		return nil, err
	}
	return notificationSDKConvert[[]notificationmodel.NotificationTemplateRecord](values)
}

func restoreRuntimeManifest(ctx context.Context, notifications appschemaapplication.InstalledNotificationTemplateCatalog, metadata manifestrepository.ManifestRuntimeMetadataRepository, seedManifest manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "restore Runtime metadata")
	return appschemaapplication.NewApplicationSchemaRuntimeRestorationApplicationService(notifications, metadata).Restore(ctx, seedManifest, scope)
}
