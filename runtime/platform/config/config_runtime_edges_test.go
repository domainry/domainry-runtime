package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateSecurityRemainingProductionGates(t *testing.T) {
	valid := Config{
		Environment: "production", AuditExportTokenKey: "audit-export-secret",
		IntegrationSecretKey: "integration-secret", IntegrationActiveKeyID: "data-1",
		CORSAllowedOrigins: []string{"https://admin.example.com"}, SchedulerPollInterval: time.Second,
	}
	setValidProductionSurfaceOrigins(&valid)
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "missing audit export key", mutate: func(c *Config) { c.AuditExportTokenKey = " " }, want: "AUDIT_EXPORT_TOKEN_KEY"},
		{name: "default audit export key", mutate: func(c *Config) { c.AuditExportTokenKey = DevAuditExportTokenKey }, want: "AUDIT_EXPORT_TOKEN_KEY"},
		{name: "missing integration key", mutate: func(c *Config) { c.IntegrationSecretKey = " " }, want: "INTEGRATION_SECRET_KEY"},
		{name: "missing integration kid", mutate: func(c *Config) { c.IntegrationActiveKeyID = " " }, want: "INTEGRATION_ACTIVE_KEY_ID"},
		{name: "scheduler interval", mutate: func(c *Config) { c.SchedulerEnabled = true; c.SchedulerPollInterval = time.Millisecond }, want: "SCHEDULER_POLL_INTERVAL"},
		{name: "insecure telemetry", mutate: func(c *Config) { c.TelemetryEndpoint = "https://otel.example.com"; c.TelemetryInsecure = true }, want: "TELEMETRY_INSECURE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.ValidateSecurity(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
	valid.Environment = "test"
	valid.RuntimeAllowDevIdentityHeaders = true
	if err := valid.ValidateSecurity(); err != nil {
		t.Fatalf("non-production security validation: %v", err)
	}
}

func TestConfigEnvironmentHelperEdges(t *testing.T) {
	if got := (Config{}).HTTPAddr(); got != ":8081" {
		t.Fatalf("default address = %q", got)
	}
	if got := (Config{Port: ":9090"}).HTTPAddr(); got != ":9090" {
		t.Fatalf("prefixed address = %q", got)
	}
	if got := (Config{Port: " 9091 "}).HTTPAddr(); got != ":9091" {
		t.Fatalf("numeric address = %q", got)
	}
	if got := (Config{HTTPBindHost: "127.0.0.1", Port: "9092"}).HTTPAddr(); got != "127.0.0.1:9092" {
		t.Fatalf("IPv4 loopback address = %q", got)
	}
	if got := (Config{HTTPBindHost: "::1", Port: "9093"}).HTTPAddr(); got != "[::1]:9093" {
		t.Fatalf("IPv6 loopback address = %q", got)
	}

	t.Setenv("CONFIG_EDGE_DURATION", "17")
	if got := durationEnv("CONFIG_EDGE_DURATION", time.Minute); got != 17*time.Second {
		t.Fatalf("numeric duration = %v", got)
	}
	t.Setenv("CONFIG_EDGE_DURATION", "invalid")
	if got := durationEnv("CONFIG_EDGE_DURATION", time.Minute); got != time.Minute {
		t.Fatalf("invalid duration = %v", got)
	}
	t.Setenv("CONFIG_EDGE_DURATION", "250ms")
	if got := durationEnv("CONFIG_EDGE_DURATION", time.Minute); got != 250*time.Millisecond {
		t.Fatalf("parsed duration = %v", got)
	}
	t.Setenv("CONFIG_EDGE_DURATION", "")
	if got := durationEnv("CONFIG_EDGE_DURATION", time.Minute); got != time.Minute {
		t.Fatalf("empty duration = %v", got)
	}

	for value, want := range map[string]bool{"1": true, "yes": true, "on": true, "0": false, "false": false, "off": false} {
		t.Setenv("CONFIG_EDGE_BOOL", value)
		if got := boolEnv("CONFIG_EDGE_BOOL", !want); got != want {
			t.Fatalf("bool %q = %v", value, got)
		}
	}
	t.Setenv("CONFIG_EDGE_BOOL", "invalid")
	if !boolEnv("CONFIG_EDGE_BOOL", true) {
		t.Fatal("invalid bool did not use fallback")
	}
	t.Setenv("CONFIG_EDGE_BOOL", "")
	if boolEnv("CONFIG_EDGE_BOOL", false) {
		t.Fatal("empty bool did not use fallback")
	}

	t.Setenv("CONFIG_EDGE_FLOAT", "invalid")
	if got := floatEnv("CONFIG_EDGE_FLOAT", .5); got != .5 {
		t.Fatalf("invalid float = %v", got)
	}
	t.Setenv("CONFIG_EDGE_FLOAT", "")
	if got := floatEnv("CONFIG_EDGE_FLOAT", .5); got != .5 {
		t.Fatalf("empty float = %v", got)
	}

	fallback := []string{"fallback"}
	t.Setenv("CONFIG_EDGE_CSV", " alpha, , beta ")
	if got := csvEnv("CONFIG_EDGE_CSV", fallback); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("csv = %#v", got)
	}
	t.Setenv("CONFIG_EDGE_CSV", " , ")
	if got := csvEnv("CONFIG_EDGE_CSV", fallback); !reflect.DeepEqual(got, fallback) {
		t.Fatalf("blank csv = %#v", got)
	}
	t.Setenv("CONFIG_EDGE_CSV", "")
	got := csvEnv("CONFIG_EDGE_CSV", fallback)
	got[0] = "changed"
	if fallback[0] != "fallback" {
		t.Fatal("CSV fallback was not cloned")
	}

	t.Setenv("CONFIG_EDGE_MAP", "one=1,invalid,=missing,empty=, two = 2")
	if got := keyMapEnv("CONFIG_EDGE_MAP"); !reflect.DeepEqual(got, map[string]string{"one": "1", "two": "2"}) {
		t.Fatalf("key map = %#v", got)
	}
	if !providerConfigured("one", " two ") || providerConfigured("one", " ") || !providerConfigured() {
		t.Fatal("provider configured contract mismatch")
	}
}

func TestProductionRedirectURLContract(t *testing.T) {
	for _, value := range []string{"", "https://login.example.com/callback", "HTTPS://login.example.com/callback"} {
		if err := validateProductionRedirectURL("OIDC_REDIRECT_URL", value); err != nil {
			t.Fatalf("value %q: %v", value, err)
		}
	}
	for _, value := range []string{"relative/callback", "://bad", "http://login.example.com/callback"} {
		if err := validateProductionRedirectURL("OIDC_REDIRECT_URL", value); err == nil || !strings.Contains(err.Error(), "OIDC_REDIRECT_URL") {
			t.Fatalf("value %q error = %v", value, err)
		}
	}
}
