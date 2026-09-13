package runtime

import (
	"fmt"
	"strings"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// notificationWorkspaceScope binds Notification to the initialized workspace.
type notificationWorkspaceScope struct {
	workspaceID    string
	applicationKey string
}

func resolveNotificationWorkspaceScope(cfg config.Config) (notificationWorkspaceScope, error) {
	workspaceID := strings.TrimSpace(cfg.NotificationWorkspaceID)
	applicationKey := strings.TrimSpace(cfg.NotificationApplicationKey)
	if workspaceID == "" || applicationKey == "" {
		return notificationWorkspaceScope{}, fmt.Errorf("Notification Workspace scope is required")
	}
	return notificationWorkspaceScope{workspaceID: workspaceID, applicationKey: applicationKey}, nil
}

func (scope notificationWorkspaceScope) applicationRef() notificationsdk.ApplicationRef {
	return notificationsdk.ApplicationRef{WorkspaceID: scope.workspaceID, ApplicationKey: scope.applicationKey}
}

func (scope notificationWorkspaceScope) publicationScope() database.NotificationSaaSPublicationScope {
	return database.NotificationSaaSPublicationScope{WorkspaceID: scope.workspaceID, ApplicationKey: scope.applicationKey}
}
