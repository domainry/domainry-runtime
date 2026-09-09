package appschema

import (
	"context"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestprojection "github.com/domainry/domainry-runtime/runtime/domain/manifest/projection"
	manifestrepository "github.com/domainry/domainry-runtime/runtime/domain/manifest/repository"
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

type ApplicationSchemaRuntimeRestorationApplicationService struct {
	notifications InstalledNotificationTemplateCatalog
	manifest      manifestrepository.ManifestRuntimeMetadataRepository
	upgradeMode   string
}

func NewApplicationSchemaRuntimeRestorationApplicationService(notifications InstalledNotificationTemplateCatalog, manifest manifestrepository.ManifestRuntimeMetadataRepository) *ApplicationSchemaRuntimeRestorationApplicationService {
	return &ApplicationSchemaRuntimeRestorationApplicationService{notifications: notifications, manifest: manifest, upgradeMode: DefinitionUpgradeModeApply}
}

// WithDefinitionUpgradeMode selects apply, verify or plan for the physical
// definition upgrade evaluated before the projection is synchronized.
func (s *ApplicationSchemaRuntimeRestorationApplicationService) WithDefinitionUpgradeMode(mode string) *ApplicationSchemaRuntimeRestorationApplicationService {
	s.upgradeMode = mode
	return s
}

// Restore evaluates the definition upgrade from the previously projected
// manifest to the installed one and, in apply mode, executes the physical DDL
// before the Metadata projection changes, so the projection never describes
// columns that do not exist yet.
func (s *ApplicationSchemaRuntimeRestorationApplicationService) Restore(ctx context.Context, installed manifestmodel.ManifestSchema, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return manifestmodel.ManifestSchema{}, metadataInternalErrorWithCause("authorize Runtime metadata restoration", err)
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return manifestmodel.ManifestSchema{}, metadataInternalError("authorize installation metadata restoration")
	}
	mode, err := normalizeDefinitionUpgradeMode(s.upgradeMode)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	previous, err := s.manifest.LoadPreviousManifest(ctx, scope)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	plan, err := s.manifest.UpgradePlan(ctx, scope, previous, installed)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	switch mode {
	case DefinitionUpgradeModePlan:
		return manifestmodel.ManifestSchema{}, &DefinitionUpgradePlanRequested{Plan: plan}
	case DefinitionUpgradeModeVerify:
		if plan.Blocking || len(plan.PendingSteps()) > 0 {
			return manifestmodel.ManifestSchema{}, definitionUpgradeError(DefinitionUpgradePendingCode, plan)
		}
	default:
		if plan.Blocking {
			return manifestmodel.ManifestSchema{}, definitionUpgradeError(DefinitionUpgradeBlockedCode, plan)
		}
		if _, err := s.manifest.ApplyUpgrade(ctx, scope, plan, installed); err != nil {
			return manifestmodel.ManifestSchema{}, err
		}
	}
	if err := s.notifications.SyncPublished(ctx, scope, installed.NotificationTemplates); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	records, err := s.notifications.List(ctx, scope)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if err = s.manifest.SyncManifestProjection(ctx, scope, installed); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	persisted, err := s.manifest.LoadManifest(ctx, scope)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if err = validateInstalledActionAuthorization(installed, persisted); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	restored := manifestprojection.MergeInstalledEnvelope(persisted, installed, activePublishedNotificationTemplates(records))
	if err = s.manifest.SyncManifest(ctx, scope, restored); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	return restored, nil
}

func activePublishedNotificationTemplates(records []notificationmodel.NotificationTemplateRecord) []notificationmodel.NotificationTemplate {
	result := make([]notificationmodel.NotificationTemplate, 0, len(records))
	for _, record := range records {
		if record.Status == "active" && record.Published != nil {
			result = append(result, *record.Published)
		}
	}
	return result
}
