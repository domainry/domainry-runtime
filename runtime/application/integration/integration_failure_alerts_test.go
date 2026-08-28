package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type zeroIntegrationJitter struct{}

func (zeroIntegrationJitter) Duration(time.Duration) time.Duration { return -time.Hour }

func TestIntegrationFailureClassificationAndAlertHelpers(t *testing.T) {
	tests := []struct {
		err  error
		want workerplatform.FailureClass
	}{
		{context.Canceled, workerplatform.FailureCancelled},
		{integrationpolicy.NewProviderError(integrationpolicy.ErrorProviderRejected, "provider.rejected", nil), workerplatform.FailureTerminal},
		{errors.New("provider rate_limited"), workerplatform.FailureRateLimited},
		{errors.New("provider connection timeout"), workerplatform.FailureDependencyUnavailable},
		{errors.New("temporary provider error"), workerplatform.FailureTransient},
	}
	for _, test := range tests {
		if got := integrationFailureClass(test.err); got != test.want {
			t.Fatalf("classify %v: got=%s want=%s", test.err, got, test.want)
		}
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{Worker: workerplatform.Dependencies{Jitter: zeroIntegrationJitter{}}})
	if delay := service.integrationRetryDelaySeconds(0); delay != 1 {
		t.Fatalf("minimum retry delay=%d", delay)
	}

	if got := stableIntegrationFailureCode(&apperror.AppError{Code: "backend.integration.failed"}, "fallback"); got != "backend.integration.failed" {
		t.Fatalf("AppError code=%q", got)
	}
	if got := stableIntegrationFailureCode(errors.New("backend.integration.raw_failure"), "fallback"); got != "backend.integration.raw_failure" {
		t.Fatalf("raw code=%q", got)
	}
	if got := stableIntegrationFailureCode(errors.New("plain failure"), "backend.integration.fallback"); got != "backend.integration.fallback" {
		t.Fatalf("fallback code=%q", got)
	}
	if got := stableIntegrationFailureCode(nil, "backend.integration.fallback"); got != "backend.integration.fallback" {
		t.Fatalf("nil fallback code=%q", got)
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	payload := failureAlertPayload(" dead_letter ", map[string]any{
		"source_type": "integration_outbox", "source_id": "message-1", "status": "failed", "attempt_count": 4,
		"error": "timeout", "password": "do-not-leak",
	}, failureAlertOptions{Recipient: " ops@example.com "}, "email", now)
	if payload["alert_kind"] != "dead_letter" || payload["severity"] != "critical" || payload["occurred_at"] != now.Format(time.RFC3339) {
		t.Fatalf("payload metadata=%#v", payload)
	}
	if strings.Contains(payload["text"].(string), "do-not-leak") || !strings.Contains(payload["text"].(string), "Source: integration_outbox message-1") {
		t.Fatalf("alert text=%q payload=%#v", payload["text"], payload)
	}
	if recipients, ok := payload["to"].([]string); !ok || len(recipients) != 1 || recipients[0] != "ops@example.com" {
		t.Fatalf("recipients=%#v", payload["to"])
	}
	plain := failureAlertPayload("event", nil, failureAlertOptions{}, "webhook", now)
	if _, found := plain["subject"]; found || plain["alert_kind"] != "event" {
		t.Fatalf("plain payload=%#v", plain)
	}

	for _, test := range []struct {
		message integrationmodel.IntegrationOutboxMessage
		want    bool
	}{
		{integrationmodel.IntegrationOutboxMessage{Operation: " integration.alert.dead_letter "}, true},
		{integrationmodel.IntegrationOutboxMessage{Payload: map[string]any{"alert_kind": "event"}}, true},
		{integrationmodel.IntegrationOutboxMessage{Payload: map[string]any{"alert_kind": nil}}, false},
		{integrationmodel.IntegrationOutboxMessage{}, false},
	} {
		if got := outboxMessageIsAlert(test.message); got != test.want {
			t.Fatalf("alert predicate message=%#v got=%v want=%v", test.message, got, test.want)
		}
	}
}

func TestFailureAlertConfig(t *testing.T) {
	for _, key := range []string{"INTEGRATION_ALERT_DISABLED", "INTEGRATION_ALERT_CONNECTION_KEY", "INTEGRATION_ALERT_CONNECTOR_KEY", "INTEGRATION_ALERT_OPERATION", "INTEGRATION_ALERT_RECIPIENT"} {
		t.Setenv(key, "")
	}
	if config := failureAlertConfig(); config.Enabled {
		t.Fatalf("empty config enabled=%+v", config)
	}
	for _, disabled := range []string{"1", "TRUE", " yes ", "on"} {
		t.Setenv("INTEGRATION_ALERT_DISABLED", disabled)
		t.Setenv("INTEGRATION_ALERT_CONNECTION_KEY", "alerts")
		if config := failureAlertConfig(); config.Enabled {
			t.Fatalf("disabled=%q config=%+v", disabled, config)
		}
	}
	t.Setenv("INTEGRATION_ALERT_DISABLED", "false")
	t.Setenv("INTEGRATION_ALERT_CONNECTION_KEY", " alerts ")
	t.Setenv("INTEGRATION_ALERT_CONNECTOR_KEY", " email ")
	t.Setenv("INTEGRATION_ALERT_OPERATION", " send ")
	t.Setenv("INTEGRATION_ALERT_RECIPIENT", " ops@example.com ")
	config := failureAlertConfig()
	if !config.Enabled || config.ConnectionKey != "alerts" || config.ConnectorKey != "email" || config.Operation != "send" || config.Recipient != "ops@example.com" {
		t.Fatalf("config=%+v", config)
	}
}

func TestEnqueueFailureAlertSkipAndSuccessPaths(t *testing.T) {
	for _, key := range []string{"INTEGRATION_ALERT_DISABLED", "INTEGRATION_ALERT_CONNECTION_KEY", "INTEGRATION_ALERT_CONNECTOR_KEY", "INTEGRATION_ALERT_OPERATION", "INTEGRATION_ALERT_RECIPIENT"} {
		t.Setenv(key, "")
	}
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"email-alerts": {Key: "email-alerts", WorkspaceID: "workspace-a", ConnectorKey: "email", ProviderKey: "smtp", Status: "verified"},
	}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	delivery := &independentDeliveryRepository{}
	audits := []string{}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, DeliveryRepository: delivery,
		Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
			{Key: "email", Type: "email", Provider: "smtp"},
			{Key: "webhook", Type: "webhook", Provider: "http"},
		}}),
		ConnectorExists: func(key string) bool { return key == "email" },
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
			audits = append(audits, event)
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "worker"}}, accessfixture.Bundle{Key: "system"})

	service.enqueueFailureAlert(t.Context(), "event", nil, "", "source-disabled", principal)
	if len(delivery.outboxes) != 0 || audits[len(audits)-1] != "integration_alert_skipped" {
		t.Fatalf("disabled outboxes=%+v audits=%v", delivery.outboxes, audits)
	}
	t.Setenv("INTEGRATION_ALERT_CONNECTION_KEY", "missing")
	service.enqueueFailureAlert(t.Context(), "event", nil, "workspace-a", "source-missing", principal)
	if len(delivery.outboxes) != 0 || audits[len(audits)-1] != "integration_alert_skipped" {
		t.Fatalf("missing connector outboxes=%+v audits=%v", delivery.outboxes, audits)
	}
	t.Setenv("INTEGRATION_ALERT_CONNECTOR_KEY", "missing")
	service.enqueueFailureAlert(t.Context(), "event", nil, "workspace-a", "source-unknown-connector", principal)
	if len(delivery.outboxes) != 0 || audits[len(audits)-1] != "integration_alert_skipped" {
		t.Fatalf("unknown connector outboxes=%+v audits=%v", delivery.outboxes, audits)
	}
	t.Setenv("INTEGRATION_ALERT_CONNECTION_KEY", "email-alerts")
	t.Setenv("INTEGRATION_ALERT_CONNECTOR_KEY", "")
	service.enqueueFailureAlert(t.Context(), "event", nil, "workspace-a", "source-recipient", principal)
	if len(delivery.outboxes) != 0 || audits[len(audits)-1] != "integration_alert_skipped" {
		t.Fatalf("missing recipient outboxes=%+v audits=%v", delivery.outboxes, audits)
	}
	t.Setenv("INTEGRATION_ALERT_RECIPIENT", "ops@example.com")
	service.enqueueFailureAlert(t.Context(), "event", map[string]any{"status": "failed"}, "workspace-a", "source-success", principal)
	if len(delivery.outboxes) != 1 || delivery.outboxes[0].ConnectorKey != "email" || delivery.outboxes[0].Operation != "email.send" || audits[len(audits)-1] != "integration_alert_enqueued" {
		t.Fatalf("success outboxes=%+v audits=%v", delivery.outboxes, audits)
	}
	delivery.insertOutboxErr = errors.New("outbox unavailable")
	service.enqueueFailureAlert(t.Context(), "event", nil, "workspace-a", "source-error", principal)
	if audits[len(audits)-1] != "integration_alert_enqueue_failed" {
		t.Fatalf("enqueue error audits=%v", audits)
	}
	delivery.outboxes = nil
	t.Setenv("INTEGRATION_ALERT_CONNECTOR_KEY", "webhook")
	t.Setenv("INTEGRATION_ALERT_OPERATION", "integration.alert")
	delivery.insertOutboxErr = nil
	service.connectorExists = func(key string) bool { return key == "email" || key == "webhook" }
	service.enqueueFailureAlert(t.Context(), "event", nil, "workspace-a", "source-webhook", principal)
	if delivery.outboxes[len(delivery.outboxes)-1].ConnectorKey != "webhook" {
		t.Fatalf("webhook alert outboxes=%+v", delivery.outboxes)
	}
}
