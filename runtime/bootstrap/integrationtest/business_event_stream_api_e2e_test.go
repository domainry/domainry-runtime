package integrationtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	notificationmodule "github.com/domainry/domainry-notification/module"
	partymodule "github.com/domainry/domainry-party/module"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
)

func TestRuntimeBusinessEventStreamConnectsReplaysAndRejectsCrossTenant(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"),
		ManifestPath:             filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"),
		UploadDir:                filepath.Join(dir, "uploads"),
		BusinessEventReplayLimit: 4, BusinessEventSubscriberBuffer: 4,
		BusinessEventGlobalConnections: 8, BusinessEventWorkspaceConnections: 4, BusinessEventPrincipalConnections: 2,
		BusinessEventHeartbeatInterval: time.Second, BusinessEventRetryInterval: 250 * time.Millisecond,
	}
	cfg = initializedIntegrationRuntimeConfig(cfg)
	application := New(t.Context(), cfg, newIntegrationIdentityBinding(t, cfg), notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), partymodule.NewFactory(partymodule.Options{}), dataexchangefixture.NewFactory())
	defer application.CloseContext(t.Context())
	server := httptest.NewServer(application.Routes())
	defer server.Close()
	client := server.Client()
	token := runtimeIdentityFixtureSession(t, "admin", "admin").AccessToken

	firstResponse, firstReader := openRuntimeEventStream(t, client, server.URL, token, "", "workspace-primary")
	heartbeat := readRuntimeSSELine(t, firstReader, func(line string) bool { return strings.HasPrefix(line, ": heartbeat") })
	if !strings.HasPrefix(heartbeat, ": heartbeat") {
		t.Fatalf("missing heartbeat: %q", heartbeat)
	}
	createRuntimeCustomer(t, client, server.URL, token, "event-one", "Event One")
	firstEvent := readRuntimeSSEEvent(t, firstReader)
	if firstEvent.Type != "refresh" || firstEvent.ObjectKey != "customer" || firstEvent.ID == "" {
		t.Fatalf("unexpected first event: %+v", firstEvent)
	}
	_ = firstResponse.Body.Close()

	createRuntimeCustomer(t, client, server.URL, token, "event-two", "Event Two")
	secondResponse, secondReader := openRuntimeEventStream(t, client, server.URL, token, firstEvent.ID, "workspace-primary")
	secondEvent := readRuntimeSSEEvent(t, secondReader)
	if secondEvent.Type != "refresh" || secondEvent.ObjectKey != "customer" || secondEvent.ID == firstEvent.ID {
		t.Fatalf("replay did not advance cursor: first=%+v second=%+v", firstEvent, secondEvent)
	}
	_ = secondResponse.Body.Close()

	crossTenantRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/events/business", nil)
	crossTenantRequest.Header.Set("Authorization", "Bearer "+token)
	crossTenantRequest.Header.Set("X-Workspace-ID", "workspace-sibling")
	crossTenantRequest.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	crossTenantResponse, err := client.Do(crossTenantRequest)
	if err != nil {
		t.Fatal(err)
	}
	crossTenantBody, _ := io.ReadAll(crossTenantResponse.Body)
	_ = crossTenantResponse.Body.Close()
	if crossTenantResponse.StatusCode != http.StatusForbidden || !bytes.Contains(crossTenantBody, []byte("backend.workspace_scope_mismatch")) {
		t.Fatalf("cross-tenant stream status=%d body=%s", crossTenantResponse.StatusCode, crossTenantBody)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		audits := runtimeFixtureAuthorizedSurfaceRequest[map[string]any](t, application.Routes(), token, "business_workspace", http.MethodGet, "/business/audit-events", nil)
		items, _ := audits["items"].([]any)
		if runtimeAuditHasEvent(items, "business_event_stream_connected") && runtimeAuditHasEvent(items, "business_event_stream_disconnected") && runtimeAuditHasEvent(items, "auth_workspace_denied") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream/workspace audit evidence missing: %#v", audits)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runtimeAuditHasEvent(audits []any, expected string) bool {
	for _, raw := range audits {
		if audit, ok := raw.(map[string]any); ok {
			if value, _ := audit["event"].(string); value == expected {
				return true
			}
		}
	}
	return false
}

type runtimeSSEEvent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ObjectKey string `json:"object_key"`
	Reason    string `json:"reason"`
}

func openRuntimeEventStream(t *testing.T, client *http.Client, baseURL, token, lastEventID, workspaceID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, baseURL+"/events/business?objects=customer", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Workspace-ID", workspaceID)
	request.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("stream status=%d body=%s", response.StatusCode, body)
	}
	return response, bufio.NewReader(response.Body)
}

func createRuntimeCustomer(t *testing.T, client *http.Client, baseURL, token, requestKey, name string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"data": map[string]any{"name": name, "owner": "admin"}})
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/objects/customer/records", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Workspace-ID", "workspace-primary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", requestKey)
	request.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("create customer status=%d body=%s", response.StatusCode, body)
	}
}

func readRuntimeSSELine(t *testing.T, reader *bufio.Reader, predicate func(string) bool) string {
	t.Helper()
	for count := 0; count < 128; count++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if predicate(line) {
			return line
		}
	}
	t.Fatal("SSE predicate was not satisfied")
	return ""
}

func readRuntimeSSEEvent(t *testing.T, reader *bufio.Reader) runtimeSSEEvent {
	t.Helper()
	id := readRuntimeSSELine(t, reader, func(line string) bool { return strings.HasPrefix(line, "id: ") })
	data := readRuntimeSSELine(t, reader, func(line string) bool { return strings.HasPrefix(line, "data: ") })
	var event runtimeSSEEvent
	if err := json.Unmarshal([]byte(strings.TrimPrefix(data, "data: ")), &event); err != nil {
		t.Fatal(err)
	}
	if event.ID == "" {
		event.ID = strings.TrimPrefix(id, "id: ")
	}
	if event.ID != strings.TrimPrefix(id, "id: ") {
		t.Fatal(fmt.Sprintf("SSE id mismatch: line=%s payload=%+v", id, event))
	}
	return event
}
