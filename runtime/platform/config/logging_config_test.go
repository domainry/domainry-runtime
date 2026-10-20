package config

import (
	"strings"
	"testing"
)

func TestLoggingConfigurationContract(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_ENCODING", "")
	cfg := FromEnv()
	if cfg.LogLevel != "info" || cfg.LogEncoding != "json" {
		t.Fatalf("logging defaults = %q/%q", cfg.LogLevel, cfg.LogEncoding)
	}
	for name, want := range map[string]string{
		"LOG_LEVEL":    "LogLevel",
		"LOG_ENCODING": "LogEncoding",
	} {
		definition, ok := definitionByName(Definitions(), name)
		if !ok || definition.Field != want || definition.Secret {
			t.Fatalf("definition %s = %#v, found=%t", name, definition, ok)
		}
	}
}

func TestLoggingConfigurationIsProcessEnvironmentOnly(t *testing.T) {
	_, _, err := LoadContract(Source{
		Name:     "remote-configuration",
		Priority: 400,
		Values:   map[string]string{"LOG_LEVEL": "debug"},
	})
	if err == nil || !strings.Contains(err.Error(), "process-environment-only") {
		t.Fatalf("remote process logging configuration error = %v", err)
	}
}

func TestLoggingConfigurationRejectsInvalidValuesFirst(t *testing.T) {
	cfg := FromEnv()
	cfg.LogLevel = "verbose"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("invalid level error = %v", err)
	}
	cfg.LogLevel = "info"
	cfg.LogEncoding = "xml"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LOG_ENCODING") {
		t.Fatalf("invalid encoding error = %v", err)
	}
}
