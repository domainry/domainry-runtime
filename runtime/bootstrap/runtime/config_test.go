package runtime

import (
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestNormalizeRuntimeConfigDoesNotInventWorkspaceScope(t *testing.T) {
	cfg := NormalizeConfig(config.Config{})
	if cfg.IdentityWorkspaceID != "" || cfg.NotificationWorkspaceID != "" || cfg.IdentityAudience != "domainry-runtime" {
		t.Fatalf("Identity defaults=%#v", cfg)
	}
}

func TestNormalizeRuntimeConfigPreservesExplicitIdentityApplication(t *testing.T) {
	cfg := NormalizeConfig(config.Config{IdentityWorkspaceID: "workspace-a", IdentityAudience: "application-a"})
	if cfg.IdentityWorkspaceID != "workspace-a" || cfg.IdentityAudience != "application-a" {
		t.Fatalf("Identity application=%#v", cfg)
	}
}

func TestNormalizeRuntimeConfigDoesNotInferDevelopmentIdentityHeaders(t *testing.T) {
	cfg := NormalizeConfig(config.Config{})
	if cfg.RuntimeAllowDevIdentityHeaders {
		t.Fatal("development Identity headers must require explicit configuration")
	}
}

func TestNormalizeRuntimeConfigSeparatesStableRuntimeScopeFromWorkerIdentity(t *testing.T) {
	cfg := NormalizeConfig(config.Config{RuntimeInstanceID: "runtime-scope", RuntimeWorkerID: "pod-a"})
	if cfg.RuntimeInstanceID != "runtime-scope" || cfg.RuntimeWorkerID != "pod-a" {
		t.Fatalf("Runtime identities=%#v", cfg)
	}

	fallback := NormalizeConfig(config.Config{RuntimeInstanceID: "single-node"})
	if fallback.RuntimeWorkerID != "single-node" {
		t.Fatalf("single-node worker identity=%q", fallback.RuntimeWorkerID)
	}
}
