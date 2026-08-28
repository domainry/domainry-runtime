package integration

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationSyncResponseHelperEdges(t *testing.T) {
	response := map[string]any{"value": "ok"}
	if got, err := validateSyncResponse(nil, response, true); err != nil || got["value"] != "ok" {
		t.Fatalf("nil schema response=%#v err=%v", got, err)
	}
	empty := &definitionmodel.ObjectSchema{}
	if got, err := validateSyncResponse(empty, response, true); err != nil || got["value"] != "ok" {
		t.Fatalf("empty schema response=%#v err=%v", got, err)
	}
	schema := &definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "", Type: "text"},
		{Key: "name", Type: "text", Required: true},
		{Key: "count", Type: "integer"},
	}}
	if _, err := validateSyncResponse(schema, map[string]any{"name": "ok", "extra": true}, true); err == nil {
		t.Fatal("strict response accepted unknown field")
	}
	if got, err := validateSyncResponse(schema, map[string]any{"name": "ok"}, true); err != nil || got["name"] != "ok" {
		t.Fatalf("strict declared response=%#v err=%v", got, err)
	}
	if _, err := validateSyncResponse(schema, map[string]any{"name": "ok", "count": map[string]any{}}, false); err == nil {
		t.Fatal("response accepted invalid field type")
	}
	if _, err := validateSyncResponse(schema, map[string]any{"count": 1}, false); err == nil {
		t.Fatal("response accepted missing required field")
	}
	if _, err := validateSyncResponse(schema, map[string]any{}, false); err == nil {
		t.Fatal("empty response accepted missing required field")
	}
	got, err := validateSyncResponse(schema, map[string]any{"name": "ok", "extra": true}, false)
	if err != nil || got["name"] != "ok" || got["extra"] != true {
		t.Fatalf("normalized response=%#v err=%v", got, err)
	}
	if metadata := syncResponseSchemaMetadata(nil, false); metadata != nil {
		t.Fatalf("nil metadata=%#v", metadata)
	}
	if metadata := syncResponseSchemaMetadata(&definitionmodel.ObjectSchema{}, false); metadata != nil {
		t.Fatalf("empty metadata=%#v", metadata)
	}
	metadata := syncResponseSchemaMetadata(schema, true)
	if metadata["strict"] != true || len(metadata["fields"].([]string)) != 2 {
		t.Fatalf("schema metadata=%#v", metadata)
	}
	if got, err := validateSyncResponse(&definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "optional", Type: "text"}}}, nil, false); err != nil || got == nil {
		t.Fatalf("nil response normalized=%#v err=%v", got, err)
	}
}

func TestIntegrationSyncPolicyAndRetryHelpers(t *testing.T) {
	if metadata := syncPolicyMetadata(SyncCallRequest{}); metadata != nil {
		t.Fatalf("empty policy metadata=%#v", metadata)
	}
	metadata := syncPolicyMetadata(SyncCallRequest{CircuitThreshold: 2, CircuitCooldown: -time.Second, RateLimitCount: 3, RateLimitWindow: -time.Second})
	if metadata["circuit_breaker"] == nil || metadata["rate_limit"] == nil {
		t.Fatalf("default policy metadata=%#v", metadata)
	}
	metadata = syncPolicyMetadata(SyncCallRequest{CircuitThreshold: 2, CircuitCooldown: 2 * time.Second, RateLimitCount: 3, RateLimitWindow: 3 * time.Second})
	if metadata["circuit_breaker"].(map[string]any)["cooldown_seconds"] != 2 || metadata["rate_limit"].(map[string]any)["window_seconds"] != 3 {
		t.Fatalf("explicit policy metadata=%#v", metadata)
	}

	tests := []struct {
		errorText   string
		responseRef string
		retry       bool
		reason      string
	}{
		{"", "", false, ""},
		{"backend.integration.sync_call.http_status_408", "", true, "http_timeout"},
		{"backend.integration.sync_call.http_status_429", "", true, "rate_limited"},
		{"backend.integration.sync_call.http_status_503", "", true, "server_error"},
		{"backend.integration.sync_call.http_status_600", "", false, "client_or_business_error"},
		{"backend.integration.sync_call.http_status_400", "", false, "client_or_business_error"},
		{"backend.integration.sync_call.http_failed", "", true, "network_error"},
		{"backend.integration.sync_call.read_failed", "", true, "response_read_failed"},
		{"backend.integration.sync_call.response_too_large", "", false, "response_too_large"},
		{"backend.integration.sync_call.invalid_json", "", false, "invalid_json"},
		{"backend.integration.sync_call.rate_limited", "", true, "rate_limited"},
		{"backend.integration.sync_call.circuit_open", "", true, "circuit_open"},
		{"backend.integration.sync_call.invalid_endpoint", "", false, "configuration_or_request_error"},
		{"backend.integration.sync_call.encode_failed", "", false, "configuration_or_request_error"},
		{"other", "http:500", true, "server_error"},
		{"other", "http:400", false, "non_retryable_error"},
	}
	for _, test := range tests {
		retry, reason := syncRetryability(test.errorText, test.responseRef)
		if retry != test.retry || reason != test.reason {
			t.Fatalf("retryability(%q,%q)=(%v,%q)", test.errorText, test.responseRef, retry, reason)
		}
	}
	if got := syncHTTPStatusFromError("backend.integration.sync_call.http_status_bad"); got != 0 {
		t.Fatalf("invalid status=%d", got)
	}
	if len(shortHash("value")) != 16 {
		t.Fatal("short hash length")
	}
}

func TestConnectionAuditShapeWrapper(t *testing.T) {
	shape := ConnectionAuditShape(integrationmodel.IntegrationConnection{Key: "connection", Config: map[string]any{"token": "secret"}})
	if shape["key"] != "connection" {
		t.Fatalf("audit shape=%#v", shape)
	}
}
