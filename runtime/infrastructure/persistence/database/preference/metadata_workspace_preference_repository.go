package preference

import (
	"context"
	"fmt"
	"strings"

	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
	preferencerepository "github.com/domainry/domainry-runtime/runtime/domain/preference/repository"
	preferencevalidation "github.com/domainry/domainry-runtime/runtime/domain/preference/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaWorkspacePreferenceRepository struct {
	metadata appschemarepository.ApplicationSchemaRepository
}

var _ preferencerepository.WorkspacePreferenceRepository = (*ApplicationSchemaWorkspacePreferenceRepository)(nil)

func NewApplicationSchemaWorkspacePreferenceRepository(metadata appschemarepository.ApplicationSchemaRepository) *ApplicationSchemaWorkspacePreferenceRepository {
	return &ApplicationSchemaWorkspacePreferenceRepository{metadata: metadata}
}

func (r *ApplicationSchemaWorkspacePreferenceRepository) ListWorkspacePreferenceVersions(ctx context.Context, scope principalmodel.QueryScope, preferenceKey string) ([]preferencemodel.WorkspacePreferenceVersion, error) {
	if !scope.Valid() || !scope.WorkspaceID().Valid() {
		return nil, fmt.Errorf("workspace preference query scope is required")
	}
	if r == nil || r.metadata == nil {
		return nil, fmt.Errorf("metadata preference source is unavailable")
	}
	preferenceKey = strings.TrimSpace(preferenceKey)
	if preferenceKey == "" {
		return nil, fmt.Errorf("preference key is required")
	}
	installation := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve workspace preference definition versions")
	current, found, err := r.metadata.GetDefinition(ctx, installation, "preference", preferenceKey)
	if err != nil {
		return nil, fmt.Errorf("read current preference %s: %w", preferenceKey, err)
	}
	if !found || strings.TrimSpace(current.DisabledAt) != "" {
		return []preferencemodel.WorkspacePreferenceVersion{}, nil
	}
	versions, err := r.metadata.ListDefinitionVersions(ctx, installation, "preference", preferenceKey)
	if err != nil {
		return nil, fmt.Errorf("list preference versions %s: %w", preferenceKey, err)
	}
	workspaceID := scope.WorkspaceID().String()
	result := make([]preferencemodel.WorkspacePreferenceVersion, 0, len(versions))
	for _, version := range versions {
		definition, decodeErr := preferencevalidation.DecodeWorkspacePreferenceDefinition(preferenceKey, version.Payload)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode preference %s version %s: %w", preferenceKey, version.SchemaVersion, decodeErr)
		}
		result = append(result, preferencemodel.WorkspacePreferenceVersion{
			WorkspaceID: workspaceID, Definition: definition, Version: version.SchemaVersion, ResourceHash: version.SchemaHash,
		})
	}
	return result, nil
}
