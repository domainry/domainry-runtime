package runtime

import (
	"fmt"
	"os"
	"strings"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// legacyNotificationWorkspaceScope is the sole adapter for the Notification
// SDK's retired TenantID-shaped transport field. Both SDK scope values are
// derived from one canonical Runtime Workspace selector.
type legacyNotificationWorkspaceScope struct {
	workspaceID    string
	applicationKey string
}

func resolveLegacyNotificationWorkspaceScope(cfg config.Config) (legacyNotificationWorkspaceScope, error) {
	workspaceID := strings.TrimSpace(cfg.NotificationWorkspaceID)
	applicationKey := strings.TrimSpace(cfg.NotificationApplicationKey)
	if workspaceID == "" || applicationKey == "" {
		return legacyNotificationWorkspaceScope{}, fmt.Errorf("Notification Workspace scope is required")
	}
	if strings.TrimSpace(os.Getenv("NOTIFICATION_TENANT_ID")) != "" {
		return legacyNotificationWorkspaceScope{}, fmt.Errorf("NOTIFICATION_TENANT_ID is retired and cannot select Notification scope")
	}
	return legacyNotificationWorkspaceScope{workspaceID: workspaceID, applicationKey: applicationKey}, nil
}

func (scope legacyNotificationWorkspaceScope) applicationRef() notificationsdk.ApplicationRef {
	return notificationsdk.ApplicationRef{TenantID: scope.workspaceID, WorkspaceID: scope.workspaceID, ApplicationKey: scope.applicationKey}
}

func (scope legacyNotificationWorkspaceScope) publicationScope() database.NotificationSaaSPublicationScope {
	return database.NotificationSaaSPublicationScope{TenantID: scope.workspaceID, WorkspaceID: scope.workspaceID, ApplicationKey: scope.applicationKey}
}
