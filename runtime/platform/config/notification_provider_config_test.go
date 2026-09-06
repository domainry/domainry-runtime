package config

import (
	"strings"
	"testing"
)

func TestNotificationApplicationScopeConfiguration(t *testing.T) {
	t.Setenv("NOTIFICATION_TENANT_ID", "tenant-a")
	t.Setenv("NOTIFICATION_WORKSPACE_ID", "workspace-a")
	t.Setenv("NOTIFICATION_APPLICATION_KEY", "orders-runtime")
	value := FromEnv()
	if value.NotificationWorkspaceID != "workspace-a" || value.NotificationApplicationKey != "orders-runtime" {
		t.Fatalf("Notification application scope=%+v", value)
	}
	if err := value.Validate(); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("legacy Notification selector error=%v", err)
	}
	t.Setenv("NOTIFICATION_TENANT_ID", "")
	candidate := FromEnv()
	candidate.NotificationWorkspaceID = ""
	if err := candidate.Validate(); err != nil {
		t.Fatalf("NOTIFICATION_WORKSPACE_ID must be resolved from the installation marker: %v", err)
	}
	missingApplication := FromEnv()
	missingApplication.NotificationApplicationKey = ""
	if err := missingApplication.Validate(); err == nil {
		t.Fatal("NOTIFICATION_APPLICATION_KEY was optional")
	}
}
