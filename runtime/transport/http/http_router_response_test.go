package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBodyEnforcesStrictSingleDocumentContract(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		ok     bool
		status int
	}{
		{name: "valid", body: `{"name":"ok"}`, ok: true, status: http.StatusOK},
		{name: "unknown field", body: `{"name":"ok","extra":true}`, status: http.StatusBadRequest},
		{name: "trailing document", body: `{"name":"ok"} {"name":"again"}`, status: http.StatusBadRequest},
		{name: "invalid", body: `{`, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &HTTPRouter{maxJSONBodyBytes: 1024, httpMetrics: NewMemoryHTTPMetricsCollector(8)}
			request := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			var target struct {
				Name string `json:"name"`
			}
			ok := router.decodeJSONBody(response, request, &target)
			if ok != test.ok {
				t.Fatalf("ok=%v want=%v body=%s", ok, test.ok, response.Body.String())
			}
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestDecodeJSONBodyRejectsOversizedBody(t *testing.T) {
	router := &HTTPRouter{maxJSONBodyBytes: 16, httpMetrics: NewMemoryHTTPMetricsCollector(8)}
	request := httptest.NewRequest(http.MethodPost, "/input", bytes.NewBufferString(`{"name":"`+strings.Repeat("x", 64)+`"}`))
	response := httptest.NewRecorder()
	var target map[string]any
	if router.decodeJSONBody(response, request, &target) {
		t.Fatal("oversized body was accepted")
	}
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "backend.request_body_too_large" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDecodeJSONBodyPreservesNumericLexemes(t *testing.T) {
	router := &HTTPRouter{maxJSONBodyBytes: 1024}
	request := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(`{"amount":0.10,"large":9007199254740993}`))
	response := httptest.NewRecorder()
	var target map[string]any
	if !router.decodeJSONBody(response, request, &target) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if target["amount"] != json.Number("0.10") || target["large"] != json.Number("9007199254740993") {
		t.Fatalf("decoded=%#v", target)
	}
}
