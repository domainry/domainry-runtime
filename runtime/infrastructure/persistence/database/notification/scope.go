package notification

import (
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func requireNotificationInstallationScope(scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return err
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return fmt.Errorf("notification installation scope is required")
	}
	return nil
}

func requireNotificationWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("notification workspace: %w", err)
	}
	return workspaceID.String(), nil
}
