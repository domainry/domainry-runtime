package runtime

import (
	"context"
	"encoding/json"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

type integrationMetricsOperationsProbe struct {
	query       integrationsdk.InvocationQuery
	invocations []integrationsdk.Invocation
}

func (*integrationMetricsOperationsProbe) Call(context.Context, integrationsdk.ProviderCallRequest) (integrationsdk.ProviderCallResult, error) {
	return integrationsdk.ProviderCallResult{}, nil
}

func (p *integrationMetricsOperationsProbe) ListInvocations(_ context.Context, query integrationsdk.InvocationQuery) ([]integrationsdk.Invocation, error) {
	p.query = query
	return append([]integrationsdk.Invocation(nil), p.invocations...), nil
}

func (*integrationMetricsOperationsProbe) GetInvocation(context.Context, string, string) (integrationsdk.Invocation, error) {
	return integrationsdk.Invocation{}, nil
}

func (*integrationMetricsOperationsProbe) AcceptWebhook(context.Context, integrationsdk.WebhookRequest) (integrationsdk.WebhookReceipt, error) {
	return integrationsdk.WebhookReceipt{}, nil
}

func (*integrationMetricsOperationsProbe) ListEvents(context.Context, integrationsdk.EventQuery) ([]integrationsdk.Event, error) {
	return nil, nil
}

func (*integrationMetricsOperationsProbe) GetEvent(context.Context, string, string) (integrationsdk.Event, error) {
	return integrationsdk.Event{}, nil
}

func (*integrationMetricsOperationsProbe) ReplayEvent(context.Context, string, string) (integrationsdk.Event, error) {
	return integrationsdk.Event{}, nil
}

func TestNotificationDeliveryMetricsReadIntegrationOwnerInvocations(t *testing.T) {
	probe := &integrationMetricsOperationsProbe{invocations: []integrationsdk.Invocation{
		{
			ID: "invocation-sent", Status: "succeeded",
			Metadata: map[string]any{"payload": map[string]any{
				"template_key": "invoice.ready", "notification_channel": "email",
			}},
		},
		{
			ID: "invocation-failed", Status: "failed", Error: "provider unavailable",
			Metadata: map[string]any{"payload": json.RawMessage(`{"template_key":"invoice.ready","notification_channel":"email","notification_fallback_root_id":"root-1"}`)},
		},
		{ID: "unrelated", Status: "succeeded", Metadata: map[string]any{"payload": map[string]any{"resource": "contact"}}},
	}}
	metrics, err := (notificationSDKDeliveryMetrics{operations: probe}).Metrics(t.Context(), "workspace-a", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if probe.query.WorkspaceID != "workspace-a" || probe.query.CreatedFrom != "2026-08-01T00:00:00Z" || probe.query.Limit != 500 {
		t.Fatalf("query=%#v", probe.query)
	}
	if metrics.Summary.Total != 2 || metrics.Summary.Sent != 1 || metrics.Summary.Failed != 1 || metrics.Summary.Fallbacks != 1 {
		t.Fatalf("summary=%#v", metrics.Summary)
	}
	if len(metrics.ByChannel) != 1 || metrics.ByChannel[0].Key != "email" || len(metrics.ByTemplate) != 1 || metrics.ByTemplate[0].Key != "invoice.ready" {
		t.Fatalf("channels=%#v templates=%#v", metrics.ByChannel, metrics.ByTemplate)
	}
	if len(metrics.Failures) != 1 || metrics.Failures[0].Error != "provider unavailable" {
		t.Fatalf("failures=%#v", metrics.Failures)
	}
}
