package metadata

import (
	"context"

	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestprojection "github.com/domainry/domainry-runtime/runtime/domain/manifest/projection"
	manifestrepository "github.com/domainry/domainry-runtime/runtime/domain/manifest/repository"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notificationprojection "github.com/domainry/domainry-runtime/runtime/domain/notification/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// InstalledNotificationTemplateCatalog is the narrow restoration boundary;
// the Runtime host adapts it to the extracted notification module store.
type InstalledNotificationTemplateCatalog interface {
	SyncPublished(context.Context, principalmodel.SystemScope, []notificationmodel.NotificationTemplate) error
	List(context.Context, principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error)
}

func PrepareInstalledManifest(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
	return manifestseed.WithGeneratedSchema(manifest)
}

type MetadataRuntimeRestorationApplicationService struct {
	notifications InstalledNotificationTemplateCatalog
	manifest      manifestrepository.ManifestRuntimeMetadataRepository
}

func NewMetadataRuntimeRestorationApplicationService(notifications InstalledNotificationTemplateCatalog, manifest manifestrepository.ManifestRuntimeMetadataRepository) *MetadataRuntimeRestorationApplicationService {
	return &MetadataRuntimeRestorationApplicationService{notifications: notifications, manifest: manifest}
}

func (s *MetadataRuntimeRestorationApplicationService) Restore(ctx context.Context, installed manifestmodel.ManifestSchema, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return manifestmodel.ManifestSchema{}, metadataInternalErrorWithCause("authorize Runtime metadata restoration", err)
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return manifestmodel.ManifestSchema{}, metadataInternalError("authorize installation metadata restoration")
	}
	if err := s.notifications.SyncPublished(ctx, scope, installed.NotificationTemplates); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	records, err := s.notifications.List(ctx, scope)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if err = s.manifest.EnsureManifestMetadata(ctx, installed); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	persisted, err := s.manifest.LoadManifestMetadata(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if err = validateInstalledActionAuthorization(installed, persisted); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	restored := manifestprojection.MergeInstalledEnvelope(persisted, installed, notificationprojection.ActivePublishedTemplates(records))
	if err = s.manifest.SyncManifestStorage(ctx, restored); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	return restored, nil
}
