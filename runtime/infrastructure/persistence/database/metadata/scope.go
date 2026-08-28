package metadata

import (
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func requireMetadataInstallationScope(scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return err
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return fmt.Errorf("metadata installation scope is required")
	}
	return nil
}

func requireMetadataWorkspaceID(explicit, embedded string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(explicit)
	if err != nil {
		return "", fmt.Errorf("metadata workspace: %w", err)
	}
	embedded = strings.TrimSpace(embedded)
	if len(embedded) > 0 && strings.Compare(workspaceID.String(), embedded) != 0 {
		return "", fmt.Errorf("metadata workspace mismatch: explicit %q does not match payload %q", workspaceID.String(), embedded)
	}
	return workspaceID.String(), nil
}
