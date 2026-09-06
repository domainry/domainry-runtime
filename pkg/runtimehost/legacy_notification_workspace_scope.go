package runtimehost

import (
	"fmt"
	"os"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// applyLegacyNotificationWorkspaceScope is the isolated compatibility adapter
// for the Notification SDK's legacy TenantID field. The value is never an
// independent authority or public Runtime configuration: it is a duplicate
// projection of the canonical Workspace ID and fails closed on disagreement.
func applyLegacyNotificationWorkspaceScope(cfg config.Config, workspaceID string) (config.Config, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || strings.TrimSpace(os.Getenv("NOTIFICATION_TENANT_ID")) != "" {
		return config.Config{}, fmt.Errorf("legacy Notification scope conflicts with initialized Workspace")
	}
	return cfg, nil
}
