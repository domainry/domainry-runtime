package config

import "testing"

func TestNotificationApplicationScopeConfiguration(t *testing.T) {
	t.Setenv("NOTIFICATION_TENANT_ID", "tenant-a")
	t.Setenv("NOTIFICATION_WORKSPACE_ID", "workspace-a")
	t.Setenv("NOTIFICATION_APPLICATION_KEY", "orders-runtime")
	value := FromEnv()
	if value.NotificationTenantID != "tenant-a" || value.NotificationWorkspaceID != "workspace-a" || value.NotificationApplicationKey != "orders-runtime" {
		t.Fatalf("Notification application scope=%+v", value)
	}
	for _, name := range []string{"NOTIFICATION_TENANT_ID", "NOTIFICATION_WORKSPACE_ID"} {
		candidate := FromEnv()
		if name == "NOTIFICATION_TENANT_ID" {
			candidate.NotificationTenantID = ""
		} else {
			candidate.NotificationWorkspaceID = ""
		}
		if err := candidate.Validate(); err != nil {
			t.Fatalf("%s must be resolved from the installation marker: %v", name, err)
		}
	}
	reserved := FromEnv()
	reserved.NotificationTenantID = "default"
	if err := reserved.Validate(); err == nil {
		t.Fatal("reserved Notification tenant was accepted")
	}
	missingApplication := FromEnv()
	missingApplication.NotificationApplicationKey = ""
	if err := missingApplication.Validate(); err == nil {
		t.Fatal("NOTIFICATION_APPLICATION_KEY was optional")
	}
}
