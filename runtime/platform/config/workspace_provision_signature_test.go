package config

import (
	"strings"
	"testing"
)

func TestWorkspaceProvisionSignatureConfigRequiresDedicatedCompleteCredential(t *testing.T) {
	for _, cfg := range []Config{
		{RuntimeWorkspaceProvisionClientID: "operations"},
		{RuntimeWorkspaceProvisionSigningSecret: strings.Repeat("s", 32)},
		{RuntimeWorkspaceProvisionClientID: "operations", RuntimeWorkspaceProvisionSigningSecret: "short"},
		{RuntimeWorkspaceProvisionClientID: "operations", RuntimeWorkspaceProvisionSigningSecret: strings.Repeat("s", 32), IntegrationSecretKey: strings.Repeat("s", 32)},
	} {
		if cfg.ValidateSecurity() == nil {
			t.Fatal("invalid signing configuration accepted")
		}
	}
	if err := (Config{}).ValidateSecurity(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNTIME_WORKSPACE_PROVISION_CLIENT_ID", "operations")
	t.Setenv("RUNTIME_WORKSPACE_PROVISION_SIGNING_SECRET", strings.Repeat("s", 32))
	cfg := FromEnv()
	if err := cfg.ValidateSecurity(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, definition := range Definitions() {
		if definition.Name == "RUNTIME_WORKSPACE_PROVISION_SIGNING_SECRET" {
			found = true
			if !definition.Secret || definition.Default != "[REDACTED]" {
				t.Fatal("signing secret was not redacted")
			}
		}
	}
	if !found {
		t.Fatal("signing secret is missing from the config contract")
	}
}
