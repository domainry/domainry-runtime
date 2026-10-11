package runtimehost

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func initialWorkspaceAdministratorCredential(cfg config.Config) (string, bool, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Environment)) {
	case "", "development":
		if cfg.AuthDefaultPassword == "" || len(cfg.AuthDefaultPassword) > 72 {
			return "", false, fmt.Errorf("AUTH_DEFAULT_PASSWORD must contain between 1 and 72 bytes in development")
		}
		return cfg.AuthDefaultPassword, cfg.AuthInitialCredentialRotationRequired, nil
	case "production":
		password, err := generateInitialWorkspacePassword()
		if err != nil {
			return "", false, err
		}
		return password, true, nil
	default:
		return "", false, fmt.Errorf("APP_ENV must be development or production")
	}
}

func generateInitialWorkspacePassword() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate initial Workspace administrator password: %w", err)
	}
	return "Dr1!" + base64.RawURLEncoding.EncodeToString(random), nil
}
