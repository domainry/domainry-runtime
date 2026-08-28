package runtime

import (
	"os"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func normalizeRuntimeConfig(cfg config.Config) config.Config {
	return normalizeRuntimeConfigWithHostname(cfg, os.Hostname)
}

func normalizeRuntimeConfigWithHostname(cfg config.Config, hostname func() (string, error)) config.Config {
	if strings.TrimSpace(cfg.IdentityWorkspaceID) == "" {
		cfg.IdentityWorkspaceID = "default"
	}
	if strings.TrimSpace(cfg.IdentityAudience) == "" {
		cfg.IdentityAudience = "domainry-runtime"
	}
	if strings.TrimSpace(cfg.RuntimeInstanceID) == "" {
		name, _ := hostname()
		if strings.TrimSpace(name) == "" {
			name = "localhost"
		}
		cfg.RuntimeInstanceID = "runtime-" + strings.TrimSpace(name) + "-" + strings.TrimPrefix(strings.TrimSpace(cfg.Port), ":")
	}
	return cfg
}
