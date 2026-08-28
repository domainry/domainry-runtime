package config

import (
	"strings"
	"testing"
)

func TestRuntimeValidatesOnlyIdentityApplicationScope(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("IDENTITY_WORKSPACE_ID", "workspace-a")
	t.Setenv("IDENTITY_AUDIENCE", "runtime-app")
	cfg := FromEnv()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"IDENTITY_MODE", "IDENTITY_ENDPOINT", "IDENTITY_ISSUER", "IDENTITY_SERVICE_ACCESS_TOKEN"} {
		if definition, ok := definitionByName(Definitions(), removed); ok {
			t.Fatalf("SaaS topology configuration leaked into Runtime contract: %#v", definition)
		}
	}

	for name, mutate := range map[string]func(*Config){
		"IDENTITY_WORKSPACE_ID": func(value *Config) { value.IdentityWorkspaceID = "" },
		"IDENTITY_AUDIENCE":     func(value *Config) { value.IdentityAudience = "" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := cfg
			mutate(&invalid)
			if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
