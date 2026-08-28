package policy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationConfigInt(config map[string]any, fallback int, keys ...string) int {
	for _, key := range keys {
		value, ok := config[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return int(parsed)
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
				return parsed
			}
		default:
			if parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(typed))); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func IntegrationProviderIdentity(connectorKey, providerKey string) string {
	return strings.TrimSpace(connectorKey) + ":" + strings.TrimSpace(providerKey)
}

func IntegrationConnectorDefinitionReady(connector integrationmodel.ConnectorSchema) bool {
	return strings.TrimSpace(connector.Key) != "" && strings.TrimSpace(connector.Type) != "" && strings.TrimSpace(connector.Provider) != ""
}

func IntegrationConnectionUsesRefreshToken(connection integrationmodel.IntegrationConnection) bool {
	return strings.TrimSpace(connection.SecretRefs["refresh_token"]) != ""
}

func IntegrationConfigBool(config map[string]any, fallback bool, keys ...string) bool {
	for _, key := range keys {
		value, ok := config[key]
		if !ok || value == nil {
			continue
		}
		if typed, ok := value.(bool); ok {
			return typed
		}
		switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

func IntegrationConfigString(config map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := config[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}
