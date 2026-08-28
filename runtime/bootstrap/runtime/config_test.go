package runtime

import (
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestNormalizeRuntimeConfigSuppliesIdentityApplicationDefaults(t *testing.T) {
	cfg := normalizeRuntimeConfig(config.Config{})
	if cfg.IdentityWorkspaceID != "default" || cfg.IdentityAudience != "domainry-runtime" {
		t.Fatalf("Identity defaults=%#v", cfg)
	}
}

func TestNormalizeRuntimeConfigPreservesExplicitIdentityApplication(t *testing.T) {
	cfg := normalizeRuntimeConfig(config.Config{IdentityWorkspaceID: "workspace-a", IdentityAudience: "application-a"})
	if cfg.IdentityWorkspaceID != "workspace-a" || cfg.IdentityAudience != "application-a" {
		t.Fatalf("Identity application=%#v", cfg)
	}
}

func TestNormalizeRuntimeConfigDoesNotInferDevelopmentIdentityHeaders(t *testing.T) {
	cfg := normalizeRuntimeConfig(config.Config{})
	if cfg.RuntimeAllowDevIdentityHeaders {
		t.Fatal("development Identity headers must require explicit configuration")
	}
}
