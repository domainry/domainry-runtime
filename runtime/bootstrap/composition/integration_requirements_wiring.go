package composition

import (
	"context"
	"fmt"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func IntegrationConnectionRequirements(values []integrationsdk.ConnectionRequirement) []integrationsdk.ConnectionRequirement {
	result := make([]integrationsdk.ConnectionRequirement, len(values))
	for index, value := range values {
		value.WorkspaceID = principalmodel.InstallationWorkspaceID
		value.Config = append([]byte(nil), value.Config...)
		result[index] = value
	}
	return result
}

// SynchronizeWorkspaceIntegrationEventMappings publishes a complete snapshot
// for one workspace. The frozen project definitions contain no tenant scope.
func SynchronizeWorkspaceIntegrationEventMappings(ctx context.Context, owner integrationsdk.Requirements, mappings []integrationsdk.EventMappingRequirement, workspaceID string) error {
	if len(mappings) == 0 {
		return nil
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return fmt.Errorf("publish Integration event mappings: %w", err)
	}
	scoped := make([]integrationsdk.EventMappingRequirement, len(mappings))
	for index, mapping := range mappings {
		mapping.WorkspaceID = workspace.String()
		scoped[index] = mapping
	}
	if err := owner.SynchronizeEventMappings(ctx, scoped); err != nil {
		return fmt.Errorf("publish Integration event mappings for Workspace %q: %w", workspace, err)
	}
	return nil
}
