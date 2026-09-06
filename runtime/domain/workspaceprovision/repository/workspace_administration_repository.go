package repository

import (
	"context"

	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type WorkspaceAdministrationRepository interface {
	ListWorkspaceCatalog(context.Context, string, int) ([]workspaceprovisionmodel.CatalogEntry, bool, error)
	SetWorkspaceStatus(context.Context, workspaceprovisionmodel.AdministrationActor, string, int, string, string) (workspaceprovisionmodel.LifecycleResult, error)
	UpdateWorkspaceCommercialConfiguration(context.Context, workspaceprovisionmodel.AdministrationActor, string, workspaceprovisionmodel.CommercialConfigurationUpdateRequest, string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, error)
}
