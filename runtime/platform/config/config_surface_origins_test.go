package config

import (
	"strings"
	"testing"
	"time"
)

func setValidProductionSurfaceOrigins(cfg *Config) {
	cfg.SurfaceBusinessOrigins = []string{"https://app.example.com"}
	cfg.SurfaceAdminOrigins = []string{"https://admin.example.com"}
	cfg.SurfacePortalOrigins = []string{"https://portal.example.com"}
	cfg.CORSAllowedOrigins = []string{
		"https://app.example.com",
		"https://admin.example.com",
		"https://portal.example.com",
	}
	cfg.HTTPPublicAddr = "0.0.0.0:8081"
	cfg.HTTPTenantAdminAddr = "127.0.0.1:8082"
	cfg.HTTPOpsAddr = "127.0.0.1:8083"
}

func TestValidateProductionSurfaceOrigins(t *testing.T) {
	valid := Config{}
	setValidProductionSurfaceOrigins(&valid)
	if err := valid.validateProductionSurfaceOrigins(); err != nil {
		t.Fatalf("valid Surface origins: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		message string
	}{
		{
			name: "every Surface must declare an origin",
			mutate: func(cfg *Config) {
				cfg.SurfacePortalOrigins = nil
			},
			message: "SURFACE_PORTAL_ORIGINS must declare",
		},
		{
			name: "origins cannot be shared across Surfaces",
			mutate: func(cfg *Config) {
				cfg.SurfacePortalOrigins = []string{"https://admin.example.com"}
			},
			message: "is shared by SURFACE_ADMIN_ORIGINS and SURFACE_PORTAL_ORIGINS",
		},
		{
			name: "production origins require HTTPS",
			mutate: func(cfg *Config) {
				cfg.SurfaceBusinessOrigins = []string{"http://app.example.com"}
			},
			message: "contains invalid production origin",
		},
		{
			name: "origins cannot contain a path",
			mutate: func(cfg *Config) {
				cfg.SurfacePortalOrigins = []string{"https://portal.example.com/login"}
			},
			message: "contains invalid production origin",
		},
		{
			name: "Surface origin must be part of global CORS policy",
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
			err := cfg.validateProductionSurfaceOrigins()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want message containing %q", err, test.message)
			}
		})
	}
}

func TestValidateProductionSurfaceListeners(t *testing.T) {
	valid := Config{
		HTTPPublicAddr:      "0.0.0.0:8081",
		HTTPTenantAdminAddr: "127.0.0.1:8082",
		HTTPOpsAddr:         "10.0.0.8:8083",
	}
	if err := valid.validateProductionSurfaceListeners(); err != nil {
		t.Fatalf("valid Surface listeners: %v", err)
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
			if err := cfg.validateProductionSurfaceListeners(); err == nil {
				t.Fatal("expected listener validation failure")
			}
		})
	}
	breakGlass := valid
	breakGlass.HTTPOpsAddr = "0.0.0.0:8083"
	breakGlass.HTTPOpsAllowPublicBindBreakGlass = true
	breakGlass.HTTPOpsPublicBindBreakGlassReason = "reviewed incident access path"
	if err := breakGlass.validateProductionSurfaceListeners(); err != nil {
		t.Fatalf("reviewed break-glass listener: %v", err)
	}
}

func TestProductionSurfaceValidationRemainingConditions(t *testing.T) {
	valid := Config{
		Environment: "production", AuditExportTokenKey: "audit-export-secret",
		IntegrationSecretKey: "integration-secret", IntegrationActiveKeyID: "data-1",
		SchedulerPollInterval: time.Second,
	}
	setValidProductionSurfaceOrigins(&valid)

	invalidOrigins := valid
	invalidOrigins.SurfacePortalOrigins = []string{"https://admin.example.com"}
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
			err := cfg.validateProductionSurfaceListeners()
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
	if err := missingReason.validateProductionSurfaceListeners(); err == nil {
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
			cfg.SurfaceBusinessOrigins = []string{test.origin}
			if err := cfg.validateProductionSurfaceOrigins(); err == nil {
				t.Fatalf("invalid origin %q accepted", test.origin)
			}
		})
	}
	sameGroupDuplicate := valid
	sameGroupDuplicate.SurfaceBusinessOrigins = []string{
		"https://app.example.com",
		"https://app.example.com",
	}
	if err := sameGroupDuplicate.validateProductionSurfaceOrigins(); err != nil {
		t.Fatalf("same-group duplicate origin error=%v", err)
	}
}
