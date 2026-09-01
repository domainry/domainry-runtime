package http

// These tests guard the shared transport error envelope used by authoring handlers.

import (
	"encoding/json"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthoringErrorResponsePreservesMachineReadableContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/metadata/definitions/connector/example/validate", nil)
	writeErrorWithParams(recorder, request, http.StatusBadRequest, "backend.integration.connector.operation_method_invalid", map[string]string{"field": "operations[0].method", "actual": "FETCH"})
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != response["message_key"] || response["field_path"] != "operations[0].method" || response["capability_key"] != nil || response["contract_version"] != capabilityapplication.RuntimeAuthoringCapabilities().ContractVersion {
		t.Fatalf("unexpected machine-readable error response: %#v", response)
	}
}

func TestAuthoringErrorResponseRedactsSensitiveParams(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/integrations/connections/example", nil)
	writeErrorWithParams(recorder, request, http.StatusBadRequest, "backend.integration.connection.invalid", map[string]string{"field_path": "connection.client_secret", "actual": "plain-secret", "connector": "crm"})
	var response struct {
		CapabilityKey string            `json:"capability_key"`
		Params        map[string]string `json:"params"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.CapabilityKey != "" || response.Params["actual"] != "[REDACTED]" || response.Params["connector"] != "crm" {
		t.Fatalf("unexpected redacted authoring response: %#v", response)
	}
}
