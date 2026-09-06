package config

import (
	"strings"
	"testing"
)

func TestWorkspaceProvisionFailurePointIsStartupOnlyAndEnvironmentGated(t *testing.T) {
	t.Setenv("WORKSPACE_PROVISION_FAILURE_POINT", "")
	for _, environment := range []string{"development", "dev", "acceptance"} {
		cfg := FromEnv()
		cfg.Environment = environment
		cfg.WorkspaceProvisionFailurePoint = "after_credential"
		if err := cfg.validateWorkspaceProvisionFailurePoint(); err != nil {
			t.Fatalf("%s rejected acceptance failure injection: %v", environment, err)
		}
	}
	for _, environment := range []string{"production", "prod", "stage", "test", "online"} {
		cfg := FromEnv()
		cfg.Environment = environment
		cfg.WorkspaceProvisionFailurePoint = "after_workspace"
		if err := cfg.validateWorkspaceProvisionFailurePoint(); err == nil || !strings.Contains(err.Error(), "development or acceptance") {
			t.Fatalf("%s failure injection gate error=%v", environment, err)
		}
	}
}

func TestWorkspaceProvisionFailurePointValidatesClosedPointCatalog(t *testing.T) {
	t.Setenv("WORKSPACE_PROVISION_FAILURE_POINT", "")
	valid := []string{
		"after_workspace", "after_identity_user", "after_identity_role",
		"after_role_assignment", "after_credential", "after_workspace_configuration",
		"after_application_bootstrap_record:store_config", "after_application_bootstrap", "after_receipt",
	}
	for _, point := range valid {
		cfg := FromEnv()
		cfg.Environment, cfg.WorkspaceProvisionFailurePoint = "acceptance", point
		if err := cfg.validateWorkspaceProvisionFailurePoint(); err != nil {
			t.Fatalf("point %q rejected: %v", point, err)
		}
	}
	for _, point := range []string{"workspace", "after_identity", "after_application_bootstrap_record:", "after_application_bootstrap_record:../store", "after_commit", "after_receipt:any"} {
		cfg := FromEnv()
		cfg.Environment, cfg.WorkspaceProvisionFailurePoint = "acceptance", point
		if err := cfg.validateWorkspaceProvisionFailurePoint(); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("point %q error=%v", point, err)
		}
	}
}

func TestWorkspaceProvisionFailurePointRejectsNonEnvironmentConfigurationSources(t *testing.T) {
	t.Setenv("WORKSPACE_PROVISION_FAILURE_POINT", "")
	for _, source := range []string{"project-configuration", "configuration-file", "secret-file", "remote-configuration"} {
		_, _, err := LoadContract(Source{Name: source, Priority: 100, Values: map[string]string{"WORKSPACE_PROVISION_FAILURE_POINT": "after_workspace"}})
		if err == nil || !strings.Contains(err.Error(), "process-environment-only") {
			t.Fatalf("source %q error=%v", source, err)
		}
	}
	configured, snapshot, err := LoadContract(Source{Name: "environment", Priority: 300, Values: map[string]string{
		"APP_ENV": "acceptance", "WORKSPACE_PROVISION_FAILURE_POINT": "after_receipt",
	}})
	if err != nil || configured.WorkspaceProvisionFailurePoint != "after_receipt" || snapshot.Entries["WORKSPACE_PROVISION_FAILURE_POINT"].Source != "environment" {
		t.Fatalf("environment startup configuration cfg=%#v snapshot=%#v err=%v", configured, snapshot.Entries["WORKSPACE_PROVISION_FAILURE_POINT"], err)
	}
}
