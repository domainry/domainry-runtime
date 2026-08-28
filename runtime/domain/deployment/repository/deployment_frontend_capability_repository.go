package repository

import (
	"context"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type DeploymentFrontendCapabilityRepository interface {
	Get(context.Context, string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error)
	Put(context.Context, string, []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error)
}
