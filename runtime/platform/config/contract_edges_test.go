package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigContractTypedSettersAndCollectionParsers(t *testing.T) {
	for field, want := range map[string]string{
		"AuditExportTokenKey": "AUDIT_EXPORT_TOKEN_KEY", "HTTPReadTimeout": "HTTP_READ_TIMEOUT", "WorkerPollInterval": "WORKER_POLL_INTERVAL",
		"AgentDialogRateLimitPerMinute": "AGENT_DIALOG_RATE_LIMIT_PER_MINUTE", "CORSAllowedOrigins": "CORS_ALLOWED_ORIGINS", "DatabaseRLSEnabled": "DATABASE_RLS_ENABLED",
		"MigrationRestoreDrillSuccessAt": "MIGRATION_RESTORE_DRILL_LAST_SUCCESS_AT",
	} {
		if got := configEnvName(field); got != want {
			t.Fatalf("configEnvName(%q)=%q want=%q", field, got, want)
		}
	}
	cfg := Config{}
	for _, test := range []struct {
		definition Definition
		raw        string
		assert     func(*testing.T, Config)
	}{
		{Definition{Field: "Port", Type: TypeString}, " 9090 ", func(t *testing.T, cfg Config) {
			t.Helper()
			if cfg.Port != "9090" {
				t.Fatalf("port=%q", cfg.Port)
			}
		}},
		{Definition{Field: "SchedulerEnabled", Type: TypeBool}, " true ", func(t *testing.T, cfg Config) {
			t.Helper()
			if !cfg.SchedulerEnabled {
				t.Fatal("bool not set")
			}
		}},
		{Definition{Field: "SchedulerBatchSize", Type: TypeInt}, " 42 ", func(t *testing.T, cfg Config) {
			t.Helper()
			if cfg.SchedulerBatchSize != 42 {
				t.Fatalf("int=%d", cfg.SchedulerBatchSize)
			}
		}},
		{Definition{Field: "TelemetrySampleRatio", Type: TypeFloat}, " 0.25 ", func(t *testing.T, cfg Config) {
			t.Helper()
			if cfg.TelemetrySampleRatio != .25 {
				t.Fatalf("float=%v", cfg.TelemetrySampleRatio)
			}
		}},
		{Definition{Field: "WorkerPollInterval", Type: TypeDuration}, " 90s ", func(t *testing.T, cfg Config) {
			t.Helper()
			if cfg.WorkerPollInterval != 90*time.Second {
				t.Fatalf("duration=%v", cfg.WorkerPollInterval)
			}
		}},
		{Definition{Field: "CORSAllowedOrigins", Type: TypeCSV}, " https://a.example, ,https://b.example ", func(t *testing.T, cfg Config) {
			t.Helper()
			if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[1] != "https://b.example" {
				t.Fatalf("csv=%#v", cfg.CORSAllowedOrigins)
			}
		}},
		{Definition{Field: "TelemetryHeaders", Type: TypeKeyMap}, " authorization=token, invalid, tenant = workspace-a ", func(t *testing.T, cfg Config) {
			t.Helper()
			if len(cfg.TelemetryHeaders) != 2 || cfg.TelemetryHeaders["tenant"] != "workspace-a" {
				t.Fatalf("key map=%#v", cfg.TelemetryHeaders)
			}
		}},
	} {
		if err := setConfigField(&cfg, test.definition, test.raw); err != nil {
			t.Fatalf("set %s: %v", test.definition.Field, err)
		}
		test.assert(t, cfg)
	}
	for _, test := range []struct {
		definition Definition
		raw        string
	}{
		{Definition{Field: "SchedulerEnabled", Type: TypeBool}, "not-bool"},
		{Definition{Field: "SchedulerBatchSize", Type: TypeInt}, "not-int"},
		{Definition{Field: "TelemetrySampleRatio", Type: TypeFloat}, "not-float"},
		{Definition{Field: "WorkerPollInterval", Type: TypeDuration}, "not-duration"},
		{Definition{Field: "Port", Type: ValueType("unknown")}, "value"},
	} {
		if err := setConfigField(&cfg, test.definition, test.raw); err == nil {
			t.Fatalf("invalid setter accepted: %+v", test)
		}
	}
	if got := splitCSV(" one, , two "); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("splitCSV=%#v", got)
	}
	if got := parseKeyMap("one=1,missing, =ignored,two = 2"); len(got) != 2 || got["one"] != "1" || got["two"] != "2" {
		t.Fatalf("parseKeyMap=%#v", got)
	}
}

func TestConfigContractProvenanceReportAndImplicitVersions(t *testing.T) {
	cfg, snapshot, err := LoadContract(Source{Name: "runtime-source", Priority: 10, Values: map[string]string{
		"PORT": "9091", "AUDIT_EXPORT_TOKEN_KEY": "runtime-secret", "TELEMETRY_HEADERS": "tenant=workspace-a",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "9091" || cfg.AuditExportTokenKey != "runtime-secret" || cfg.TelemetryHeaders["tenant"] != "workspace-a" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if snapshot.Entries["AUDIT_EXPORT_TOKEN_KEY"].Version != "unversioned" || !snapshot.Entries["AUDIT_EXPORT_TOKEN_KEY"].Redacted || snapshot.Entries["PORT"].Version != valueVersion("9091") {
		t.Fatalf("entries=%+v", snapshot.Entries)
	}
	report := snapshot.StartupReport()
	if len(report) != len(snapshot.Entries) {
		t.Fatalf("report=%d entries=%d", len(report), len(snapshot.Entries))
	}
	for index := 1; index < len(report); index++ {
		if report[index-1].Name > report[index].Name {
			t.Fatalf("report not sorted at %d: %+v", index, report)
		}
	}
}

func TestConfigValidationRejectsEachRuntimeBoundary(t *testing.T) {
	valid, _, err := LoadContract()
	if err != nil {
		t.Fatal(err)
	}
	valid.Environment = "development"
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"HTTP timeout", func(cfg *Config) { cfg.HTTPReadTimeout = 0 }, "HTTP timeouts"},
		{"scheduler batch", func(cfg *Config) { cfg.SchedulerBatchSize = 0 }, "SCHEDULER_BATCH_SIZE"},
		{"scheduler lease", func(cfg *Config) { cfg.SchedulerLeaseTTL = cfg.SchedulerPollInterval }, "SCHEDULER_LEASE_TTL"},
		{"empty port", func(cfg *Config) { cfg.Port = " " }, "PORT is required"},
		{"invalid port", func(cfg *Config) { cfg.Port = ":70000" }, "PORT must be"},
		{"invalid bind host", func(cfg *Config) { cfg.HTTPBindHost = "127.0.0.1:8081" }, "HTTP_BIND_HOST"},
		{"JSON body", func(cfg *Config) { cfg.HTTPMaxJSONBodyBytes = 1 }, "HTTP_MAX_JSON_BODY_BYTES"},
		{"header bytes", func(cfg *Config) { cfg.HTTPMaxHeaderBytes = 1 }, "HTTP_MAX_HEADER_BYTES"},
		{"global in flight", func(cfg *Config) { cfg.CapacityGlobalInFlight = 0 }, "CAPACITY_GLOBAL_IN_FLIGHT"},
		{"workspace in flight", func(cfg *Config) { cfg.CapacityWorkspaceInFlight = cfg.CapacityGlobalInFlight + 1 }, "CAPACITY_WORKSPACE_IN_FLIGHT"},
		{"use case in flight", func(cfg *Config) { cfg.CapacityUseCaseInFlight = cfg.CapacityGlobalInFlight + 1 }, "CAPACITY_USE_CASE_IN_FLIGHT"},
		{"retry in flight", func(cfg *Config) { cfg.CapacityRetryInFlight = cfg.CapacityGlobalInFlight }, "CAPACITY_RETRY_IN_FLIGHT"},
		{"rate budgets", func(cfg *Config) { cfg.CapacityWorkspaceRatePerMinute = cfg.CapacityGlobalRatePerMinute + 1 }, "capacity rate budgets"},
		{"state limits", func(cfg *Config) { cfg.CapacityMaxWorkspaceStates = 0 }, "capacity state"},
		{"hysteresis", func(cfg *Config) { cfg.CapacityRecoveryRatio = cfg.CapacityDegradedRatio }, "hysteresis"},
		{"connector in flight", func(cfg *Config) { cfg.CapacityConnectorWorkspaceInFlight = cfg.CapacityConnectorGlobalInFlight + 1 }, "connector in-flight"},
		{"connector rate", func(cfg *Config) {
			cfg.CapacityConnectorProviderRatePerMinute = cfg.CapacityConnectorGlobalRatePerMinute + 1
		}, "connector rate"},
		{"queue threshold", func(cfg *Config) { cfg.CapacityQueueDepthThreshold = 0 }, "queue backpressure"},
		{"telemetry ratio", func(cfg *Config) { cfg.TelemetrySampleRatio = 2 }, "TELEMETRY_SAMPLE_RATIO"},
		{"database pool", func(cfg *Config) { cfg.DatabaseMaxIdleConns = cfg.DatabaseMaxOpenConns + 1 }, "database pool"},
		{"database timeout", func(cfg *Config) { cfg.DatabaseLockTimeout = 0 }, "database timeouts"},
		{"required paths", func(cfg *Config) { cfg.UploadDir = "" }, "paths are required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestConfigSourceFilesAndUnknownWarning(t *testing.T) {
	if values, version, err := readConfigSourceFile("CONFIG_EDGE_UNSET"); err != nil || len(values) != 0 || version != "" {
		t.Fatalf("unset values=%#v version=%q err=%v", values, version, err)
	}
	t.Setenv("CONFIG_EDGE_MISSING", filepath.Join(t.TempDir(), "missing.json"))
	if _, _, err := readConfigSourceFile("CONFIG_EDGE_MISSING"); err == nil || !strings.Contains(err.Error(), "read CONFIG_EDGE_MISSING") {
		t.Fatalf("missing file err=%v", err)
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"PORT":`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_EDGE_INVALID", invalidPath)
	if _, _, err := readConfigSourceFile("CONFIG_EDGE_INVALID"); err == nil || !strings.Contains(err.Error(), "decode CONFIG_EDGE_INVALID") {
		t.Fatalf("invalid file err=%v", err)
	}
	validPath := filepath.Join(t.TempDir(), "valid.json")
	if err := os.WriteFile(validPath, []byte(`{"PORT":"9191"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_EDGE_VALID", validPath)
	values, version, err := readConfigSourceFile("CONFIG_EDGE_VALID")
	if err != nil || values["PORT"] != "9191" || version == "" {
		t.Fatalf("values=%#v version=%q err=%v", values, version, err)
	}

	t.Setenv("APP_ENV", "development")
	t.Setenv("RUNTIME_UNKNOWN_EDGE", "value")
	t.Setenv("CONFIG_UNKNOWN_POLICY", "warn")
	_, snapshot, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) == 0 || !strings.Contains(snapshot.Warnings[0], "RUNTIME_UNKNOWN_EDGE") {
		t.Fatalf("warnings=%#v", snapshot.Warnings)
	}
}

func TestConfigLoadSourceAndSecretFileErrors(t *testing.T) {
	t.Run("configuration file", func(t *testing.T) {
		t.Setenv("RUNTIME_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.json"))
		if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "RUNTIME_CONFIG_FILE") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("remote file", func(t *testing.T) {
		t.Setenv("RUNTIME_CONFIG_FILE", "")
		t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.json"))
		if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "RUNTIME_REMOTE_CONFIG_FILE") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("missing secret file", func(t *testing.T) {
		t.Setenv("RUNTIME_CONFIG_FILE", "")
		t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", "")
		t.Setenv("AUDIT_EXPORT_TOKEN_KEY_FILE", filepath.Join(t.TempDir(), "missing.secret"))
		if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUDIT_EXPORT_TOKEN_KEY_FILE") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("secret file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.secret")
		if err := os.WriteFile(path, []byte(" file-secret \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("RUNTIME_CONFIG_FILE", "")
		t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", "")
		t.Setenv("AUDIT_EXPORT_TOKEN_KEY_FILE", path)
		cfg, snapshot, err := Load()
		if err != nil || cfg.AuditExportTokenKey != "file-secret" || snapshot.Entries["AUDIT_EXPORT_TOKEN_KEY"].Source != "secret-file" {
			t.Fatalf("secret=%q entry=%+v err=%v", cfg.AuditExportTokenKey, snapshot.Entries["AUDIT_EXPORT_TOKEN_KEY"], err)
		}
	})
	t.Run("invalid environment value", func(t *testing.T) {
		t.Setenv("RUNTIME_CONFIG_FILE", "")
		t.Setenv("RUNTIME_REMOTE_CONFIG_FILE", "")
		t.Setenv("SCHEDULER_BATCH_SIZE", "not-int")
		if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "SCHEDULER_BATCH_SIZE") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestConfigManagedNamesAndSecurityValidationEdges(t *testing.T) {
	for _, name := range []string{"RUNTIME_CONFIG_FILE", "RUNTIME_REMOTE_CONFIG_FILE", "RUNTIME_CONFIG_VERSION"} {
		if managedConfigName(name) {
			t.Fatalf("control variable %q treated as managed value", name)
		}
	}
	if !managedConfigName("PORT") || !managedConfigName("AUDIT_EXPORT_TOKEN_KEY") || managedConfigName("PATH") {
		t.Fatal("managed configuration classification mismatch")
	}
	valid, _, err := LoadContract()
	if err != nil {
		t.Fatal(err)
	}
	valid.Environment = "production"
	valid.RuntimeAllowDevIdentityHeaders = true
	if err := valid.Validate(); err == nil || !strings.Contains(err.Error(), "RUNTIME_ALLOW_DEV_IDENTITY_HEADERS") {
		t.Fatalf("security validation error=%v", err)
	}
}
