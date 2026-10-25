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
	t.Setenv("LOG_DIAGNOSTIC_ENABLED", "")
	t.Setenv("LOG_DIAGNOSTIC_FEATURES", "")
	t.Setenv("LOG_HTTP_CONTENT", "")
	t.Setenv("LOG_SQL_STATEMENT", "")
	t.Setenv("LOG_CONTENT_MAX_BYTES", "")
	t.Setenv("LOG_DIAGNOSTIC_POLICY_FILE", "")
	cfg := FromEnv()
	if cfg.LogLevel != "info" || cfg.LogEncoding != "json" || !cfg.LogConsole || cfg.LogDirectory != "" || cfg.LogMaxFileSizeMB != 100 || cfg.LogRetentionDays != 30 || cfg.LogMaxTotalSizeMB != 2048 || !cfg.LogDiagnosticEnabled || !cfg.LogHTTPContent || !cfg.LogSQLStatement || cfg.LogContentMaxBytes != 16<<10 || len(cfg.LogDiagnosticFeatures) != 10 || cfg.LogDiagnosticPolicyFile != "" {
		t.Fatalf("logging defaults = %+v", cfg)
	}
	for name, want := range map[string]string{
		"LOG_LEVEL":                  "LogLevel",
		"LOG_ENCODING":               "LogEncoding",
		"LOG_CONSOLE":                "LogConsole",
		"LOG_DIRECTORY":              "LogDirectory",
		"LOG_MAX_FILE_SIZE_MB":       "LogMaxFileSizeMB",
		"LOG_RETENTION_DAYS":         "LogRetentionDays",
		"LOG_MAX_TOTAL_SIZE_MB":      "LogMaxTotalSizeMB",
		"LOG_DIAGNOSTIC_ENABLED":     "LogDiagnosticEnabled",
		"LOG_DIAGNOSTIC_FEATURES":    "LogDiagnosticFeatures",
		"LOG_HTTP_CONTENT":           "LogHTTPContent",
		"LOG_SQL_STATEMENT":          "LogSQLStatement",
		"LOG_CONTENT_MAX_BYTES":      "LogContentMaxBytes",
		"LOG_DIAGNOSTIC_POLICY_FILE": "LogDiagnosticPolicyFile",
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
		Values:   map[string]string{"LOG_LEVEL": "debug", "LOG_DIRECTORY": "/var/log/runtime", "LOG_HTTP_CONTENT": "false"},
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
	cfg.LogEncoding = "json"
	cfg.LogContentMaxBytes = 65537
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LOG_CONTENT_MAX_BYTES") {
		t.Fatalf("invalid content limit error = %v", err)
	}
	cfg.LogContentMaxBytes = 1024
	cfg.LogDiagnosticFeatures = []string{"sql", "unknown"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "LOG_DIAGNOSTIC_FEATURES") {
		t.Fatalf("invalid diagnostic feature error = %v", err)
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
