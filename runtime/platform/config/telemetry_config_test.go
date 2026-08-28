package config

import (
	"testing"
	"time"
)

func TestTelemetryConfigLoadsExporterAndCredentials(t *testing.T) {
	t.Setenv("TELEMETRY_EXPORTER", "otlp-http")
	t.Setenv("TELEMETRY_ENDPOINT", "https://otel.example.test/v1/traces")
	t.Setenv("TELEMETRY_HEADERS", "Authorization=Bearer opaque,X-Tenant=runtime")
	t.Setenv("TELEMETRY_SAMPLE_RATIO", "0.25")
	t.Setenv("TELEMETRY_EXPORT_TIMEOUT", "3s")
	cfg := FromEnv()
	if cfg.TelemetryExporter != "otlp-http" || cfg.TelemetryEndpoint == "" || cfg.TelemetryHeaders["Authorization"] != "Bearer opaque" || cfg.TelemetrySampleRatio != 0.25 || cfg.TelemetryExportTimeout != 3*time.Second {
		t.Fatalf("unexpected telemetry config: %#v", cfg)
	}
}

func TestProductionRejectsInsecureTelemetryTransport(t *testing.T) {
	cfg := Config{Environment: "production", AuditExportTokenKey: "safe-audit-export", IntegrationSecretKey: "safe-integration", IntegrationActiveKeyID: "data-1", TelemetryEndpoint: "http://collector:4318/v1/traces", TelemetryInsecure: true}
	setValidProductionSurfaceOrigins(&cfg)
	if err := cfg.ValidateSecurity(); err == nil {
		t.Fatal("production accepted insecure telemetry transport")
	}
}

func TestOperationalEvidenceTimestampsLoadFromEnvironment(t *testing.T) {
	t.Setenv("MIGRATION_BACKUP_LAST_SUCCESS_AT", "2026-07-18T12:00:00Z")
	t.Setenv("MIGRATION_RESTORE_DRILL_LAST_SUCCESS_AT", "2026-07-10T12:00:00Z")
	cfg := FromEnv()
	if cfg.MigrationBackupLastSuccessAt != "2026-07-18T12:00:00Z" || cfg.MigrationRestoreDrillSuccessAt != "2026-07-10T12:00:00Z" {
		t.Fatalf("operational evidence timestamps not loaded: %#v", cfg)
	}
}
