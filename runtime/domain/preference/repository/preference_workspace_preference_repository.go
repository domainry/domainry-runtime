package repository

import (
	"context"

	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// WorkspacePreferenceRepository exposes only workspace-scoped version reads.
// Persistence adapters must not infer a workspace from an empty scope.
type WorkspacePreferenceRepository interface {
	ListWorkspacePreferenceVersions(context.Context, principalmodel.QueryScope, string) ([]preferencemodel.WorkspacePreferenceVersion, error)
}
