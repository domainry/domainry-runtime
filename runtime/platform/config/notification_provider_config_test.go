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
	for name, clear := range map[string]func(*Config){
		"NOTIFICATION_TENANT_ID":       func(value *Config) { value.NotificationTenantID = "" },
		"NOTIFICATION_WORKSPACE_ID":    func(value *Config) { value.NotificationWorkspaceID = "" },
		"NOTIFICATION_APPLICATION_KEY": func(value *Config) { value.NotificationApplicationKey = "" },
	} {
		t.Run(name+" required", func(t *testing.T) {
			candidate := FromEnv()
			clear(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("%s was optional", name)
			}
		})
	}
}
