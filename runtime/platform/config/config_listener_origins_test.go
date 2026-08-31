package config

import (
	"strings"
	"testing"
	"time"
)

func setValidProductionListenerOrigins(cfg *Config) {
	cfg.HTTPPublicOrigins = []string{"https://app.example.com"}
	cfg.HTTPTenantAdminOrigins = []string{"https://admin.example.com"}
	cfg.HTTPOpsOrigins = []string{"https://ops.example.com"}
	cfg.CORSAllowedOrigins = []string{
		"https://app.example.com",
		"https://admin.example.com",
		"https://ops.example.com",
	}
	cfg.HTTPPublicAddr = "0.0.0.0:8081"
	cfg.HTTPTenantAdminAddr = "127.0.0.1:8082"
	cfg.HTTPOpsAddr = "127.0.0.1:8083"
}

func TestValidateProductionListenerOrigins(t *testing.T) {
	valid := Config{}
	setValidProductionListenerOrigins(&valid)
	if err := valid.validateProductionListenerOrigins(); err != nil {
		t.Fatalf("valid Listener origins: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		message string
	}{
		{
			name: "every listener must declare an origin",
			mutate: func(cfg *Config) {
				cfg.HTTPOpsOrigins = nil
			},
			message: "HTTP_OPS_ORIGINS must declare",
		},
		{
			name: "origins cannot be shared across listeners",
			mutate: func(cfg *Config) {
				cfg.HTTPOpsOrigins = []string{"https://admin.example.com"}
			},
			message: "is shared by HTTP_TENANT_ADMIN_ORIGINS and HTTP_OPS_ORIGINS",
		},
		{
			name: "production origins require HTTPS",
			mutate: func(cfg *Config) {
				cfg.HTTPPublicOrigins = []string{"http://app.example.com"}
			},
			message: "contains invalid production origin",
		},
		{
			name: "origins cannot contain a path",
			mutate: func(cfg *Config) {
				cfg.HTTPOpsOrigins = []string{"https://portal.example.com/login"}
			},
			message: "contains invalid production origin",
		},
		{
			name: "Listener origin must be part of global CORS policy",
			mutate: func(cfg *Config) {
				cfg.CORSAllowedOrigins = []string{
					"https://app.example.com",
					"https://admin.example.com",
				}
			},
			message: "is missing from CORS_ALLOWED_ORIGINS",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			err := cfg.validateProductionListenerOrigins()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want message containing %q", err, test.message)
			}
		})
	}
}

func TestValidateProductionListeners(t *testing.T) {
	valid := Config{
		HTTPPublicAddr:      "0.0.0.0:8081",
		HTTPTenantAdminAddr: "127.0.0.1:8082",
		HTTPOpsAddr:         "10.0.0.8:8083",
	}
	if err := valid.validateProductionListeners(); err != nil {
		t.Fatalf("valid Listener listeners: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing listener", func(cfg *Config) { cfg.HTTPTenantAdminAddr = "" }},
		{"shared listener", func(cfg *Config) { cfg.HTTPOpsAddr = cfg.HTTPTenantAdminAddr }},
		{"public Ops listener", func(cfg *Config) { cfg.HTTPOpsAddr = "0.0.0.0:8083" }},
		{"invalid listener", func(cfg *Config) { cfg.HTTPPublicAddr = "localhost" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.validateProductionListeners(); err == nil {
				t.Fatal("expected listener validation failure")
			}
		})
	}
	breakGlass := valid
	breakGlass.HTTPOpsAddr = "0.0.0.0:8083"
	breakGlass.HTTPOpsAllowPublicBindBreakGlass = true
	breakGlass.HTTPOpsPublicBindBreakGlassReason = "reviewed incident access path"
	if err := breakGlass.validateProductionListeners(); err != nil {
		t.Fatalf("reviewed break-glass listener: %v", err)
	}
}

func TestProductionListenerValidationRemainingConditions(t *testing.T) {
	valid := Config{
		Environment: "production", AuditExportTokenKey: "audit-export-secret",
		IntegrationSecretKey: "integration-secret", IntegrationActiveKeyID: "data-1",
		SchedulerPollInterval: time.Second,
	}
	setValidProductionListenerOrigins(&valid)

	invalidOrigins := valid
	invalidOrigins.HTTPOpsOrigins = []string{"https://admin.example.com"}
	if err := invalidOrigins.ValidateSecurity(); err == nil || !strings.Contains(err.Error(), "shared by") {
		t.Fatalf("ValidateSecurity origin error=%v", err)
	}
	invalidListeners := valid
	invalidListeners.HTTPOpsAddr = "0.0.0.0:8083"
	if err := invalidListeners.ValidateSecurity(); err == nil || !strings.Contains(err.Error(), "HTTP_OPS_ADDR") {
		t.Fatalf("ValidateSecurity listener error=%v", err)
	}

	for _, test := range []struct {
		name string
		addr string
	}{
		{name: "empty port", addr: "127.0.0.1:"},
		{name: "hostname with break glass", addr: "ops.example.com:8083"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			cfg.HTTPOpsAddr = test.addr
			cfg.HTTPOpsAllowPublicBindBreakGlass = true
			cfg.HTTPOpsPublicBindBreakGlassReason = "reviewed"
			err := cfg.validateProductionListeners()
			if test.name == "empty port" && err == nil {
				t.Fatal("empty listener port accepted")
			}
			if test.name != "empty port" && err != nil {
				t.Fatalf("hostname listener error=%v", err)
			}
		})
	}
	missingReason := valid
	missingReason.HTTPOpsAddr = "0.0.0.0:8083"
	missingReason.HTTPOpsAllowPublicBindBreakGlass = true
	if err := missingReason.validateProductionListeners(); err == nil {
		t.Fatal("public listener without break-glass reason accepted")
	}

	for _, test := range []struct {
		name   string
		origin string
	}{
		{name: "parse", origin: "https://%gh"},
		{name: "host", origin: "https:"},
		{name: "user", origin: "https://user@app.example.com"},
		{name: "query", origin: "https://app.example.com?mode=admin"},
		{name: "fragment", origin: "https://app.example.com#admin"},
	} {
		t.Run("origin_"+test.name, func(t *testing.T) {
			cfg := valid
			cfg.HTTPPublicOrigins = []string{test.origin}
			if err := cfg.validateProductionListenerOrigins(); err == nil {
				t.Fatalf("invalid origin %q accepted", test.origin)
			}
		})
	}
	sameGroupDuplicate := valid
	sameGroupDuplicate.HTTPPublicOrigins = []string{
		"https://app.example.com",
		"https://app.example.com",
	}
	if err := sameGroupDuplicate.validateProductionListenerOrigins(); err != nil {
		t.Fatalf("same-group duplicate origin error=%v", err)
	}
}
