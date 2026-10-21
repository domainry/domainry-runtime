package config

import (
	"strings"
	"testing"
)

func TestLoggingConfigurationContract(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_ENCODING", "")
	t.Setenv("LOG_CONSOLE", "")
	t.Setenv("LOG_DIRECTORY", "")
	t.Setenv("LOG_MAX_FILE_SIZE_MB", "")
	t.Setenv("LOG_RETENTION_DAYS", "")
	t.Setenv("LOG_MAX_TOTAL_SIZE_MB", "")
	cfg := FromEnv()
	if cfg.LogLevel != "info" || cfg.LogEncoding != "json" || !cfg.LogConsole || cfg.LogDirectory != "" || cfg.LogMaxFileSizeMB != 100 || cfg.LogRetentionDays != 30 || cfg.LogMaxTotalSizeMB != 2048 {
		t.Fatalf("logging defaults = %+v", cfg)
	}
	for name, want := range map[string]string{
		"LOG_LEVEL":             "LogLevel",
		"LOG_ENCODING":          "LogEncoding",
		"LOG_CONSOLE":           "LogConsole",
		"LOG_DIRECTORY":         "LogDirectory",
		"LOG_MAX_FILE_SIZE_MB":  "LogMaxFileSizeMB",
		"LOG_RETENTION_DAYS":    "LogRetentionDays",
		"LOG_MAX_TOTAL_SIZE_MB": "LogMaxTotalSizeMB",
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
		Values:   map[string]string{"LOG_LEVEL": "debug", "LOG_DIRECTORY": "/var/log/runtime"},
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

func TestLoggingConfigurationRequiresAUsableOutputAndBoundedArchives(t *testing.T) {
	cfg := FromEnv()
	cfg.LogConsole = false
	cfg.LogDirectory = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LOG_CONSOLE=false requires LOG_DIRECTORY") {
		t.Fatalf("missing logging output error = %v", err)
	}
	cfg.LogDirectory = "/var/log/runtime"
	cfg.LogMaxFileSizeMB = 100
	cfg.LogMaxTotalSizeMB = 99
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "total size") {
		t.Fatalf("invalid archive bounds error = %v", err)
	}
}
