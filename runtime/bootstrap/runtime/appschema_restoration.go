package runtime

import (
	"context"
	"fmt"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
)

func initializeRuntimeProjectModel(ctx context.Context, store *persistence.RuntimeStore, model projectmodel.RuntimeModel) (appschemapersistence.ApplicationSchemaStore, error) {
	metadataStore := appschemapersistence.NewApplicationSchemaStore(store)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "initialize project model")
	if err := metadataStore.InitializeProjectModel(ctx, scope, model); err != nil {
		return appschemapersistence.ApplicationSchemaStore{}, err
	}
	return metadataStore, nil
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
