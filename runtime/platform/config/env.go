package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func floatEnv(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func keyValueEnv(key, fallback string) map[string]string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	result := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		keyValue := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(keyValue) == 2 && strings.TrimSpace(keyValue[0]) != "" {
			result[strings.TrimSpace(keyValue[0])] = strings.TrimSpace(keyValue[1])
		}
	}
	return result
}

func csvEnv(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), fallback...)
	}
	return out
}

func keyMapEnv(key string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(strings.TrimSpace(os.Getenv(key)), ",") {
		id, value, ok := strings.Cut(item, "=")
		id, value = strings.TrimSpace(id), strings.TrimSpace(value)
		if ok && id != "" && value != "" {
			out[id] = value
		}
	}
	return out
}

func providerConfigured(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func validateProductionRedirectURL(key string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute HTTPS URL in production", key)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%s must use HTTPS in production", key)
	}
	return nil
}
