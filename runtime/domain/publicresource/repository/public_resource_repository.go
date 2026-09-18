package repository

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	publicresourcemodel "github.com/domainry/domainry-runtime/runtime/domain/publicresource/model"
)

// Repository performs only the exact cross-workspace capability-key lookup
// declared by a manifest public resource. It is intentionally not a generic
// anonymous Record repository.
type Repository interface {
	Find(context.Context, definitionmodel.ObjectSchema, definitionmodel.ObjectPublicResource, string) (publicresourcemodel.Projection, bool, error)
}
