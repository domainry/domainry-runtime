package repository

import (
	"context"

	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type WorkspaceProvisionRepository interface {
	Provision(context.Context, workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error)
}
