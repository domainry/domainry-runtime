package notification

import (
	"context"
	"fmt"
	"time"

	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// ManifestTemplateCatalog adapts Runtime manifest restoration to the module
// store. It owns no template SQL or lifecycle behavior.
type ManifestTemplateCatalog struct{ store *database.RuntimeStore }

func NewManifestTemplateCatalog(store *database.RuntimeStore) ManifestTemplateCatalog {
	return ManifestTemplateCatalog{store: store}
}

func (c ManifestTemplateCatalog) SyncPublished(ctx context.Context, scope principalmodel.SystemScope, values []notificationmodel.NotificationTemplate) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeInstallation {
		return fmt.Errorf("notification manifest template installation scope is required")
	}
	store, err := NewSQLStoreAdapter(c.store, manifestTemplateClock{})
	if err != nil {
		return err
	}
	templates := make([]sourcetemplate.Template, len(values))
	for i, v := range values {
		templates[i] = notificationcontract.ModuleTemplate(v)
	}
	return store.SyncPublished(ctx, templates)
}

func (c ManifestTemplateCatalog) List(ctx context.Context, scope principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeInstallation {
		return nil, fmt.Errorf("notification manifest template installation scope is required")
	}
	store, err := NewSQLStoreAdapter(c.store, manifestTemplateClock{})
	if err != nil {
		return nil, err
	}
	values, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]notificationmodel.NotificationTemplateRecord, len(values))
	for i, v := range values {
		result[i] = notificationcontract.PlaneTemplateRecord(v)
	}
	return result, nil
}

type manifestTemplateClock struct{}

func (manifestTemplateClock) Now() time.Time { return time.Now().UTC() }
