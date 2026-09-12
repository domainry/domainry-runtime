package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

const (
	g04ConnectorKey  = "reminder_delivery"
	g04ProviderKey   = "fixture"
	g04ConnectionKey = "reminder_messages"
	g04OperationKey  = "send_message"
)

type g04DeliveryPayload struct {
	TemplateKey          string           `json:"template_key"`
	TemplateVersion      int              `json:"template_version"`
	TemplateLocale       string           `json:"template_locale"`
	TemplateContentHash  string           `json:"template_content_hash"`
	VariablesHash        string           `json:"variables_hash"`
	NotificationChannel  string           `json:"notification_channel"`
	NotificationProvider string           `json:"notification_provider"`
	NotificationMetadata map[string]any   `json:"notification_metadata"`
	Recipient            string           `json:"recipient"`
	Message              string           `json:"message"`
	Facts                []map[string]any `json:"facts"`
	Actions              []map[string]any `json:"actions"`
	NotificationContent  map[string]any   `json:"notification_content"`
}

type g04DeliveryProbe struct {
	mu         sync.Mutex
	calls      int
	recipient  string
	message    string
	requestRef string
}

func (p *g04DeliveryProbe) record(_ context.Context, request connector.TypedRequest[g04DeliveryPayload]) (connector.DeliveryResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.recipient, p.message, p.requestRef = request.Input.Recipient, request.Input.Message, request.RequestRef
	return connector.DeliveryResult{ResponseRef: "fixture-reminder:" + request.Input.Recipient}, nil
}

func (p *g04DeliveryProbe) snapshot() (int, string, string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.recipient, p.message, p.requestRef
}

func g04DeliveryAdapter(t *testing.T, probe *g04DeliveryProbe) connector.Adapter {
	t.Helper()
	operation, err := connector.BindEnqueueDelivery(connector.EnqueueOperation[g04DeliveryPayload]{
		ConnectorKey: g04ConnectorKey, ProviderKey: g04ProviderKey, Key: g04OperationKey,
		ContractSHA256: "4444444444444444444444444444444444444444444444444444444444444444",
		Reliability: connector.ReliabilityContract{
			Effect:         connector.EffectWrite,
			Idempotency:    connector.IdempotencyContract{Strategy: connector.IdempotencyProviderKey, KeyRetentionSeconds: 86400},
			Reconciliation: connector.ReconciliationNone,
			Compensation:   connector.CompensationContract{Mode: connector.CompensationNone},
		},
	}, probe.record)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := connector.NewProvider(connector.ProviderSchema{
		ConnectorKey: g04ConnectorKey, ProviderKey: g04ProviderKey, ProviderRevision: "g04-fixture-v1", StartupActivation: connector.StartupActivationDefaultSafe,
	}, operation)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestScheduledReminderSignedCallbackMaterializesInboxDeliversConnectedChannelAndDeduplicates(t *testing.T) {
	temp := t.TempDir()
	manifestPath := g04ScheduledReminderManifest(t, temp)
	cfg := initializedIntegrationRuntimeConfig(config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: manifestPath, UploadDir: filepath.Join(temp, "uploads"),
		RuntimeInstanceID: "g04-runtime", IdentityAudience: "domainry-runtime", IntegrationSecretKey: "g04-runtime-integration-signing-secret", WorkerPollInterval: 5 * time.Millisecond, WorkerBatchSize: 25,
	})
	identity := newIntegrationIdentityBinding(t, cfg)
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	probe := &g04DeliveryProbe{}
	providers := connector.NewRegistry()
	if err := providers.RegisterProviderSet(connector.ProviderSet{Providers: []connector.Adapter{g04DeliveryAdapter(t, probe)}}); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	runtime := bootstrap.NewWithExtensions(t.Context(), cfg, handlers, providers, identity, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory())
	bootstrap.StartWorkers(t.Context(), runtime)
	defer runtime.CloseContext(t.Context())
	handler := notificationModuleRoutes(t, runtime)

	dueAt := time.Now().UTC().Truncate(time.Millisecond)
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace-primary", UserID: "runtime_fixture_user", ProductKey: "business_only_minimal"}
	payload, err := json.Marshal(schedulersdk.ScheduledPlanDispatch{
		ContractVersion: schedulersdk.ScheduledPlanDispatchContractVersion, PlanID: "plan-g04-reminder", Owner: owner,
		Input: json.RawMessage(`{"title":"交周报","message":"请在下班前提交周报。"}`), AllowedActions: []string{"notification.reminder.publish"},
	})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "plan-g04-reminder:" + dueAt.Format(time.RFC3339Nano)
	body, err := json.Marshal(map[string]any{
		"runtime_id": cfg.RuntimeInstanceID, "execution_id": "scheduler-g04-run", "idempotency_key": idempotencyKey, "due_at": dueAt,
		"target": map[string]any{"type": "runtime_operation", "owner": "notification", "operation": "publish_reminder", "payload": json.RawMessage(payload)},
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func() (int, map[string]any) {
		request := httptest.NewRequest(http.MethodPost, dispatchhttp.RuntimeExecutionPath, bytes.NewReader(body))
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature, signErr := schedulergateway.SignRequest(body, schedulergateway.SignedRequest{
			Method: http.MethodPost, Path: dispatchhttp.RuntimeExecutionPath, RuntimeID: cfg.RuntimeInstanceID, IdempotencyKey: idempotencyKey,
		}, schedulergateway.SchedulerClientID, timestamp, []byte(cfg.IntegrationSecretKey))
		if signErr != nil {
			t.Fatal(signErr)
		}
		request.Header.Set(schedulergateway.RuntimeIDHeader, cfg.RuntimeInstanceID)
		request.Header.Set(schedulergateway.SignatureVersionHeader, schedulergateway.CallbackSignatureContractVersion)
		request.Header.Set(schedulergateway.ClientIDHeader, schedulergateway.SchedulerClientID)
		request.Header.Set(schedulergateway.TimestampHeader, timestamp)
		request.Header.Set(schedulergateway.SignatureHeader, signature)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		decoded := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return response.Code, decoded
	}

	status, receipt := send()
	if status != http.StatusOK || receipt["owner"] != "notification" || receipt["replay"] == true {
		t.Fatalf("first callback status=%d receipt=%#v", status, receipt)
	}
	item := waitProjectNotification(t, handler, owner.UserID)
	if item["event_type"] != "scheduler.reminder.due" || item["recipient_user_id"] != owner.UserID || item["title"] != "交周报" || item["body"] != "请在下班前提交周报。" {
		t.Fatalf("inbox item=%#v", item)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls, recipient, message, requestRef := probe.snapshot()
		if calls == 1 {
			if recipient != owner.UserID || message != "交周报\n\n请在下班前提交周报。" || requestRef == "" {
				t.Fatalf("external delivery calls=%d recipient=%q message=%q request_ref=%q", calls, recipient, message, requestRef)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if calls, _, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("external delivery calls=%d", calls)
	}
	deadline = time.Now().Add(5 * time.Second)
	var deliveries []map[string]any
	var observedDeliveries []map[string]any
	for time.Now().Before(deadline) {
		code, responseBody := projectNotificationRequest(handler, owner.UserID, "sales_manager", http.MethodGet, "/notification/deliveries?limit=20", "", nil)
		if code != http.StatusOK {
			t.Fatalf("delivery ledger status=%d body=%s", code, responseBody)
		}
		var page struct {
			Deliveries []map[string]any `json:"deliveries"`
		}
		if json.Unmarshal([]byte(responseBody), &page) == nil {
			observedDeliveries = page.Deliveries
			if len(page.Deliveries) == 1 && page.Deliveries[0]["status"] == "accepted" {
				deliveries = page.Deliveries
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(deliveries) != 1 || deliveries[0]["connector_key"] != g04ConnectorKey || deliveries[0]["connection_key"] != g04ConnectionKey || deliveries[0]["operation"] != g04OperationKey || deliveries[0]["response_ref"] == "" {
		t.Fatalf("delivery ledger=%#v observed=%#v", deliveries, observedDeliveries)
	}
	code, invocationBody := projectNotificationRequest(handler, owner.UserID, "sales_manager", http.MethodGet, "/integration/invocations?connector_key="+g04ConnectorKey+"&connection_key="+g04ConnectionKey+"&operation="+g04OperationKey, "", nil)
	if code != http.StatusOK {
		t.Fatalf("Integration invocation ledger status=%d body=%s", code, invocationBody)
	}
	var invocationPage struct {
		Invocations []map[string]any `json:"invocations"`
	}
	if json.Unmarshal([]byte(invocationBody), &invocationPage) != nil || len(invocationPage.Invocations) != 1 || invocationPage.Invocations[0]["status"] != "succeeded" || invocationPage.Invocations[0]["response_ref"] != "fixture-reminder:"+owner.UserID {
		t.Fatalf("Integration invocation ledger=%#v", invocationPage.Invocations)
	}

	status, receipt = send()
	if status != http.StatusOK || receipt["replay"] != true {
		t.Fatalf("replay callback status=%d receipt=%#v", status, receipt)
	}
	time.Sleep(100 * time.Millisecond)
	if calls, _, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("replay duplicated provider delivery calls=%d", calls)
	}
	if items := projectNotificationList(t, handler, owner.UserID); len(items) != 1 {
		t.Fatalf("replay duplicated inbox items=%#v", items)
	}
}

func g04ScheduledReminderManifest(t *testing.T, directory string) string {
	t.Helper()
	source := filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("decode base manifest")
	}
	variables := []any{
		map[string]any{"key": "title", "type": "string", "required": true},
		map[string]any{"key": "message", "type": "text", "required": true},
		map[string]any{"key": "scheduled_for", "type": "datetime", "required": true},
	}
	manifest["notification_event_types"] = []any{map[string]any{
		"key": "scheduler.reminder.due", "source": "scheduler", "category": "reminder", "default_severity": "info", "mandatory_in_app": true,
		"template_key": "scheduler.reminder.due.in_app", "default_locale": "en-US", "variables": variables,
		"locales": map[string]any{"en-US": map[string]any{"title": "{{title}}", "body": "{{message}}", "facts": []any{map[string]any{"key": "Scheduled", "value": "{{scheduled_for}}"}}}},
		"version": 1, "status": "published",
	}}
	manifest["notification_templates"] = []any{map[string]any{
		"key": "scheduler.reminder.due.collaboration", "name": "Scheduled reminder", "channel": "collaboration", "provider": "slack", "status": "published", "version": 1,
		"default_locale": "en-US", "variables": []any{
			map[string]any{"key": "title", "type": "text", "required": true},
			map[string]any{"key": "message", "type": "text", "required": true},
			map[string]any{"key": "scheduled_for", "type": "datetime", "required": true},
		}, "locales": map[string]any{"en-US": map[string]any{"title": "{{title}}", "markdown": "{{message}}"}},
	}}
	manifest["notification_rules"] = []any{map[string]any{
		"event_type_key": "scheduler.reminder.due", "enabled": true, "mandatory_in_app": true, "minimum_severity": "info", "user_mutable": true,
		"channels": []any{
			map[string]any{"channel": "in_app", "mandatory": true},
			map[string]any{"channel": "collaboration", "template_key": "scheduler.reminder.due.collaboration", "connector_key": g04ConnectorKey, "connection_key": g04ConnectionKey, "operation": g04OperationKey},
		},
	}}
	manifest["integrations"] = map[string]any{"connections": []any{map[string]any{
		"key": g04ConnectionKey, "connector_key": g04ConnectorKey, "provider_key": g04ProviderKey, "name": "Reminder messages", "status": "active", "config": map[string]any{},
	}}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "g04-reminder-manifest.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
