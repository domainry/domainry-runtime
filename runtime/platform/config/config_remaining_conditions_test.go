package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigHelperAndSecurityRemainingConditions(t *testing.T) {
	if mode := (Config{DatabaseMigrationMode: " APPLY "}).EffectiveDatabaseMigrationMode(); mode != "apply" {
		t.Fatalf("explicit migration mode=%q", mode)
	}
	if mode := (Config{Environment: "production"}).EffectiveDatabaseMigrationMode(); mode != "verify" {
		t.Fatalf("production migration mode=%q", mode)
	}
	secure := Config{
		Environment: "production", AuditExportTokenKey: "audit-export-secret",
		IntegrationSecretKey: "integration-secret", IntegrationActiveKeyID: "data-1",
		CORSAllowedOrigins: []string{"https://admin.example.com"}, SchedulerPollInterval: time.Second,
	}
	setValidProductionListenerOrigins(&secure)
	sharedSecret := secure
	sharedSecret.IntegrationSecretKey = sharedSecret.AuditExportTokenKey
	if err := sharedSecret.ValidateSecurity(); err == nil || !strings.Contains(err.Error(), "INTEGRATION_SECRET_KEY") {
		t.Fatalf("shared secret error=%v", err)
	}
	enabledScheduler := secure
	enabledScheduler.SchedulerEnabled = true
	if err := enabledScheduler.ValidateSecurity(); err != nil {
		t.Fatalf("valid scheduler security=%v", err)
	}
	secureTelemetry := secure
	secureTelemetry.TelemetryEndpoint = "https://otel.example.com"
	if err := secureTelemetry.ValidateSecurity(); err != nil {
		t.Fatalf("secure telemetry=%v", err)
	}

	t.Setenv("CONFIG_EDGE_DURATION_ZERO", "-1")
	if got := durationEnv("CONFIG_EDGE_DURATION_ZERO", time.Minute); got != time.Minute {
		t.Fatalf("zero duration=%v", got)
	}
	t.Setenv("CONFIG_EDGE_INT_NEGATIVE", "-1")
	if got := intEnv("CONFIG_EDGE_INT_NEGATIVE", 7); got != 7 {
		t.Fatalf("negative int=%d", got)
	}
	t.Setenv("CONFIG_EDGE_KEY_VALUE", " =ignored,valid=value")
	if got := keyValueEnv("CONFIG_EDGE_KEY_VALUE", ""); len(got) != 1 || got["valid"] != "value" {
		t.Fatalf("key values=%v", got)
	}
	if err := validateProductionRedirectURL("REDIRECT", "https:/callback"); err == nil {
		t.Fatal("hostless HTTPS redirect accepted")
	}
}

func TestConfigLoadWithoutUnknownsAndSourceStatFailure(t *testing.T) {
	known := map[string]bool{}
	for _, definition := range Definitions() {
		known[definition.Name] = true
	}
	type savedEnvironment struct {
		name  string
		value string
	}
	saved := []savedEnvironment{}
	for _, item := range os.Environ() {
		name, value, _ := strings.Cut(item, "=")
		base := strings.TrimSuffix(name, "_FILE")
		if managedConfigName(name) && !known[base] {
			saved = append(saved, savedEnvironment{name: name, value: value})
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, item := range saved {
			_ = os.Setenv(item.name, item.value)
		}
	})
	if _, snapshot, err := Load(); err != nil || len(snapshot.Warnings) != 0 {
		t.Fatalf("clean load warnings=%v err=%v", snapshot.Warnings, err)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"PORT":"9090"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_EDGE_STAT", path)
	previousStat := configSourceStat
	configSourceStat = func(string) (os.FileInfo, error) { return nil, errors.New("stat failed") }
	t.Cleanup(func() { configSourceStat = previousStat })
	if _, _, err := readConfigSourceFile("CONFIG_EDGE_STAT"); err == nil || !strings.Contains(err.Error(), "stat CONFIG_EDGE_STAT") {
		t.Fatalf("stat error=%v", err)
	}
}

func TestOptionalProjectConfigFileStatAndTypeFailures(t *testing.T) {
	previous := optionalProjectConfigLstat
	optionalProjectConfigLstat = func(string) (os.FileInfo, error) { return nil, errors.New("lstat failed") }
	t.Cleanup(func() { optionalProjectConfigLstat = previous })
	if _, _, err := readOptionalProjectConfigFile("config.json"); err == nil {
		t.Fatal("lstat failure was ignored")
	}
	optionalProjectConfigLstat = os.Lstat
	if _, _, err := readOptionalProjectConfigFile(t.TempDir()); err == nil {
		t.Fatal("directory project config was accepted")
	}
}

func TestConfigValidateEveryShortCircuitOperand(t *testing.T) {
	valid, _, err := LoadContract()
	if err != nil {
		t.Fatal(err)
	}
	valid.Environment = "development"
	for _, host := range []string{"localhost", "127.0.0.1"} {
		hostConfig := valid
		hostConfig.HTTPBindHost = host
		if err := hostConfig.Validate(); err != nil {
			t.Fatalf("valid bind host %q: %v", host, err)
		}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"read header timeout", func(c *Config) { c.HTTPReadHeaderTimeout = 0 }},
		{"write timeout", func(c *Config) { c.HTTPWriteTimeout = 0 }},
		{"idle timeout", func(c *Config) { c.HTTPIdleTimeout = 0 }},
		{"shutdown timeout", func(c *Config) { c.HTTPShutdownTimeout = 0 }},
		{"scheduler batch high", func(c *Config) { c.SchedulerBatchSize = 1001 }},
		{"port syntax", func(c *Config) { c.Port = "not-a-port" }},
		{"port zero", func(c *Config) { c.Port = "0" }},
		{"body high", func(c *Config) { c.HTTPMaxJSONBodyBytes = (64 << 20) + 1 }},
		{"global inflight high", func(c *Config) { c.CapacityGlobalInFlight = 100001 }},
		{"workspace inflight zero", func(c *Config) { c.CapacityWorkspaceInFlight = 0 }},
		{"use case inflight zero", func(c *Config) { c.CapacityUseCaseInFlight = 0 }},
		{"retry inflight zero", func(c *Config) { c.CapacityRetryInFlight = 0 }},
		{"global rate zero", func(c *Config) { c.CapacityGlobalRatePerMinute = 0 }},
		{"global rate high", func(c *Config) { c.CapacityGlobalRatePerMinute = 10_000_001 }},
		{"workspace rate zero", func(c *Config) { c.CapacityWorkspaceRatePerMinute = 0 }},
		{"use case rate zero", func(c *Config) { c.CapacityUseCaseRatePerMinute = 0 }},
		{"use case rate high", func(c *Config) { c.CapacityUseCaseRatePerMinute = c.CapacityGlobalRatePerMinute + 1 }},
		{"use case states zero", func(c *Config) { c.CapacityMaxUseCaseStates = 0 }},
		{"workspace state ttl zero", func(c *Config) { c.CapacityWorkspaceStateTTL = 0 }},
		{"request timeout zero", func(c *Config) { c.CapacityRequestTimeout = 0 }},
		{"retry after zero", func(c *Config) { c.CapacityRetryAfter = 0 }},
		{"degraded ratio zero", func(c *Config) { c.CapacityDegradedRatio = 0 }},
		{"degraded ratio one", func(c *Config) { c.CapacityDegradedRatio = 1 }},
		{"recovery ratio zero", func(c *Config) { c.CapacityRecoveryRatio = 0 }},
		{"connector global inflight zero", func(c *Config) { c.CapacityConnectorGlobalInFlight = 0 }},
		{"connector workspace inflight zero", func(c *Config) { c.CapacityConnectorWorkspaceInFlight = 0 }},
		{"connector provider inflight zero", func(c *Config) { c.CapacityConnectorProviderInFlight = 0 }},
		{"connector provider inflight high", func(c *Config) { c.CapacityConnectorProviderInFlight = c.CapacityConnectorGlobalInFlight + 1 }},
		{"connector global rate zero", func(c *Config) { c.CapacityConnectorGlobalRatePerMinute = 0 }},
		{"connector workspace rate zero", func(c *Config) { c.CapacityConnectorWorkspaceRatePerMinute = 0 }},
		{"connector provider rate zero", func(c *Config) { c.CapacityConnectorProviderRatePerMinute = 0 }},
		{"queue depth high", func(c *Config) { c.CapacityQueueDepthThreshold = 1_000_001 }},
		{"queue age zero", func(c *Config) { c.CapacityQueueOldestAgeThreshold = 0 }},
		{"telemetry ratio negative", func(c *Config) { c.TelemetrySampleRatio = -0.1 }},
		{"database open zero", func(c *Config) { c.DatabaseMaxOpenConns = 0 }},
		{"database idle negative", func(c *Config) { c.DatabaseMaxIdleConns = -1 }},
		{"database lifetime zero", func(c *Config) { c.DatabaseConnMaxLifetime = 0 }},
		{"database idle time zero", func(c *Config) { c.DatabaseConnMaxIdleTime = 0 }},
		{"database connect zero", func(c *Config) { c.DatabaseConnectTimeout = 0 }},
		{"database statement zero", func(c *Config) { c.DatabaseStatementTimeout = 0 }},
		{"manifest path empty", func(c *Config) { c.ManifestPath = " " }},
		{"migration path empty", func(c *Config) { c.MigrationDir = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("invalid config accepted: %+v", cfg)
			}
		})
	}
}

func TestConfigUnknownProductionPolicyCondition(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("RUNTIME_ALLOW_DEV_IDENTITY_HEADERS", "false")
	t.Setenv("AUDIT_EXPORT_TOKEN_KEY", "production-audit-export-secret")
	t.Setenv("INTEGRATION_SECRET_KEY", "production-integration-secret")
	t.Setenv("INTEGRATION_ACTIVE_KEY_ID", "data-1")
	t.Setenv("HTTP_PUBLIC_ORIGINS", "https://app.example.com")
	t.Setenv("HTTP_MANAGEMENT_ORIGINS", "https://admin.example.com")
	t.Setenv("HTTP_OPS_ORIGINS", "https://ops.example.com")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.com,https://admin.example.com,https://ops.example.com")
	t.Setenv("HTTP_PUBLIC_ADDR", "0.0.0.0:8081")
	t.Setenv("HTTP_MANAGEMENT_ADDR", "127.0.0.1:8082")
	t.Setenv("HTTP_OPS_ADDR", "127.0.0.1:8083")
	t.Setenv("RUNTIME_UNKNOWN_PRODUCTION_EDGE", "value")
	if _, _, err := Load(); err == nil || !strings.Contains(err.Error(), "RUNTIME_UNKNOWN_PRODUCTION_EDGE") {
		t.Fatalf("unknown production configuration error=%v", err)
	}
}
