package config

import (
	"strings"
	"testing"
)

func TestInstallationAdministratorBootstrapIsDefaultOffCompleteAndEnvironmentOnly(t *testing.T) {
	for _, name := range []string{
		"INSTALLATION_ADMINISTRATOR_BOOTSTRAP_ENABLED", "INSTALLATION_ADMINISTRATOR_REQUEST_ID",
		"INSTALLATION_ADMINISTRATOR_LOGIN_ID", "INSTALLATION_ADMINISTRATOR_NAME", "INSTALLATION_ADMINISTRATOR_CREDENTIAL_FILE",
	} {
		t.Setenv(name, "")
	}
	base, _, err := LoadContract()
	if err != nil || base.InstallationAdministratorBootstrapEnabled {
		t.Fatalf("default config enabled=%t error=%v", base.InstallationAdministratorBootstrapEnabled, err)
	}
	if _, _, err := LoadContract(Source{Name: "project-configuration", Values: map[string]string{
		"INSTALLATION_ADMINISTRATOR_BOOTSTRAP_ENABLED": "true",
	}}); err == nil || !strings.Contains(err.Error(), "process-environment-only") {
		t.Fatalf("project source error=%v", err)
	}
	incomplete := base
	incomplete.InstallationAdministratorBootstrapEnabled = true
	if err := incomplete.Validate(); err == nil || !strings.Contains(err.Error(), "INSTALLATION_ADMINISTRATOR_REQUEST_ID") {
		t.Fatalf("incomplete config error=%v", err)
	}
	partialDisabled := base
	partialDisabled.InstallationAdministratorLoginID = "admin@example.test"
	if err := partialDisabled.Validate(); err == nil || !strings.Contains(err.Error(), "requires INSTALLATION_ADMINISTRATOR_BOOTSTRAP_ENABLED") {
		t.Fatalf("disabled partial config error=%v", err)
	}
	complete := base
	complete.InstallationAdministratorBootstrapEnabled = true
	complete.InstallationAdministratorRequestID = "first-installation-administrator"
	complete.InstallationAdministratorLoginID = "admin@example.test"
	complete.InstallationAdministratorName = "Installation Administrator"
	complete.InstallationAdministratorCredentialFile = "/private/installation-administrator.json"
	if err := complete.Validate(); err != nil {
		t.Fatalf("complete explicit config error=%v", err)
	}
}
