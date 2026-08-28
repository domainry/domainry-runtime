package policy

import (
	"encoding/json"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type integrationNumericString string

func TestIntegrationConfigIntMatrix(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]any
		fallback int
		keys     []string
		want     int
	}{
		{name: "missing", config: nil, fallback: 9, keys: []string{"value"}, want: 9},
		{name: "nil then value", config: map[string]any{"first": nil, "second": 2}, fallback: 9, keys: []string{"missing", "first", "second"}, want: 2},
		{name: "int64", config: map[string]any{"value": int64(3)}, keys: []string{"value"}, want: 3},
		{name: "float64", config: map[string]any{"value": 4.9}, keys: []string{"value"}, want: 4},
		{name: "json number", config: map[string]any{"value": json.Number("5")}, keys: []string{"value"}, want: 5},
		{name: "invalid json then fallback key", config: map[string]any{"value": json.Number("bad"), "next": "6"}, keys: []string{"value", "next"}, want: 6},
		{name: "string", config: map[string]any{"value": " 7 "}, keys: []string{"value"}, want: 7},
		{name: "invalid string", config: map[string]any{"value": "bad"}, fallback: 8, keys: []string{"value"}, want: 8},
		{name: "stringer default", config: map[string]any{"value": []byte("10")}, fallback: 8, keys: []string{"value"}, want: 8},
		{name: "default conversion", config: map[string]any{"value": integrationNumericString("10")}, fallback: 8, keys: []string{"value"}, want: 10},
		{name: "numeric default", config: map[string]any{"value": true, "next": 11}, fallback: 8, keys: []string{"value", "next"}, want: 11},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IntegrationConfigInt(test.config, test.fallback, test.keys...); got != test.want {
				t.Fatalf("int = %d, want %d", got, test.want)
			}
		})
	}
}

func TestIntegrationConfigDefinitionAndSecretPolicies(t *testing.T) {
	if got := IntegrationProviderIdentity(" connector ", " provider "); got != "connector:provider" {
		t.Fatalf("adapter key = %q", got)
	}
	valid := integrationmodel.ConnectorSchema{Key: "key", Type: "type", Provider: "provider"}
	if !IntegrationConnectorDefinitionReady(valid) {
		t.Fatal("complete connector should be ready")
	}
	for _, connector := range []integrationmodel.ConnectorSchema{
		{Type: "type", Provider: "provider"},
		{Key: "key", Provider: "provider"},
		{Key: "key", Type: "type"},
	} {
		if IntegrationConnectorDefinitionReady(connector) {
			t.Fatalf("incomplete connector is ready: %+v", connector)
		}
	}
	if IntegrationConnectionUsesRefreshToken(integrationmodel.IntegrationConnection{}) || IntegrationConnectionUsesRefreshToken(integrationmodel.IntegrationConnection{SecretRefs: map[string]string{"refresh_token": " "}}) {
		t.Fatal("missing refresh token should be false")
	}
	if !IntegrationConnectionUsesRefreshToken(integrationmodel.IntegrationConnection{SecretRefs: map[string]string{"refresh_token": " secret "}}) {
		t.Fatal("refresh token should be detected")
	}
}

func TestIntegrationConfigBoolAndStringMatrix(t *testing.T) {
	for _, test := range []struct {
		value any
		want  bool
	}{{true, true}, {false, false}, {1, true}, {"TRUE", true}, {" yes ", true}, {"y", true}, {"on", true}, {0, false}, {"FALSE", false}, {"no", false}, {"n", false}, {"off", false}} {
		if got := IntegrationConfigBool(map[string]any{"value": test.value}, !test.want, "value"); got != test.want {
			t.Fatalf("bool(%#v) = %v, want %v", test.value, got, test.want)
		}
	}
	if !IntegrationConfigBool(map[string]any{"nil": nil, "bad": "unknown"}, true, "missing", "nil", "bad") {
		t.Fatal("unknown boolean should preserve fallback")
	}
	if IntegrationConfigBool(map[string]any{"bad": "unknown", "next": "true"}, false, "bad", "next") != true {
		t.Fatal("unknown boolean should continue to later key")
	}
	if got := IntegrationConfigString(map[string]any{"nil": nil, "empty": " ", "nil-text": "<nil>", "number": 12}, "missing", "nil", "empty", "nil-text", "number"); got != "12" {
		t.Fatalf("config string = %q", got)
	}
	if got := IntegrationConfigString(nil, "missing"); got != "" {
		t.Fatalf("missing config string = %q", got)
	}
}
