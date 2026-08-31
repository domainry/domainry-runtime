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

	withoutWorkspace := cfg
	withoutWorkspace.IdentityWorkspaceID = ""
	if err := withoutWorkspace.Validate(); err != nil {
		t.Fatalf("durable installation workspace may be unresolved before database startup: %v", err)
	}
	reserved := cfg
	reserved.IdentityWorkspaceID = "default"
	if err := reserved.Validate(); err == nil || !strings.Contains(err.Error(), "IDENTITY_WORKSPACE_ID") {
		t.Fatalf("reserved workspace error=%v", err)
	}
	withoutAudience := cfg
	withoutAudience.IdentityAudience = ""
	if err := withoutAudience.Validate(); err == nil || !strings.Contains(err.Error(), "IDENTITY_AUDIENCE") {
		t.Fatalf("missing audience error=%v", err)
	}
}
