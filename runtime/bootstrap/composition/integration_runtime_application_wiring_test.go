package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationservice "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type publicationDeliveryProbe struct {
	accepted int
	queried  int
	err      error
}

func (p *publicationDeliveryProbe) Accept(_ context.Context, request integrationsdk.DeliveryRequest) (integrationsdk.DeliveryReceipt, error) {
	p.accepted++
	return integrationsdk.DeliveryReceipt{MessageID: request.MessageID, InvocationID: "external", Status: integrationsdk.DeliveryStatusAccepted}, p.err
}

func (p *publicationDeliveryProbe) Query(context.Context, string) (integrationsdk.DeliveryReceipt, error) {
	p.queried++
	return integrationsdk.DeliveryReceipt{InvocationID: "external", Status: integrationsdk.DeliveryStatusSucceeded}, p.err
}

func TestRuntimePublicationDeliveryKeepsExternalTrafficOnIntegrationOwner(t *testing.T) {
	probe := &publicationDeliveryProbe{}
	delivery := runtimePublicationDelivery{external: probe}
	payload, _ := json.Marshal(map[string]any{"value": true})
	receipt, err := delivery.Accept(t.Context(), integrationsdk.DeliveryRequest{
		MessageID: "message", DeduplicationKey: "dedupe", WorkspaceID: "workspace", ConnectorKey: "google", Operation: "send", Payload: payload,
	})
	if err != nil || receipt.InvocationID != "external" || probe.accepted != 1 {
		t.Fatalf("receipt=%+v accepted=%d err=%v", receipt, probe.accepted, err)
	}
	if _, err = delivery.Query(t.Context(), "external"); err != nil || probe.queried != 1 {
		t.Fatalf("queried=%d err=%v", probe.queried, err)
	}
}

func TestRuntimePublicationDeliveryFailsClosedWhenAutomationRuntimeUnavailable(t *testing.T) {
	probe := &publicationDeliveryProbe{err: errors.New("external must not be called")}
	delivery := runtimePublicationDelivery{external: probe}
	payload, _ := json.Marshal(map[string]any{"rule_key": "rule"})
	_, err := delivery.Accept(t.Context(), integrationsdk.DeliveryRequest{
		MessageID: "message", DeduplicationKey: "dedupe", WorkspaceID: "workspace", ConnectorKey: runtimeAutomationConnectorKey, Operation: "rule", Payload: payload,
	})
	if err == nil || probe.accepted != 0 {
		t.Fatalf("automation delivery did not fail closed: accepted=%d err=%v", probe.accepted, err)
	}
}

type runtimeAutomationRegistry struct {
	rules map[string]automationmodel.AutomationRuleSchema
}

func (r runtimeAutomationRegistry) List() []automationmodel.AutomationRuleSchema {
	result := make([]automationmodel.AutomationRuleSchema, 0, len(r.rules))
	for _, rule := range r.rules {
		result = append(result, rule)
	}
	return result
}

func (r runtimeAutomationRegistry) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	rule, found := r.rules[key]
	return rule, found
}

func TestRuntimePublicationDeliveryExecutesAutomationLocally(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "after-order-update", ObjectKey: "order", Enabled: true,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
	}
	service := automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{
		Rules: runtimeAutomationRegistry{rules: map[string]automationmodel.AutomationRuleSchema{rule.Key: rule}},
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, RoleKey: roleKey}}
		},
	})
	probe := &publicationDeliveryProbe{err: errors.New("external must not be called")}
	delivery := runtimePublicationDelivery{external: probe, records: &runtimeAssembly{automationApplicationService: service}}
	payload, _ := json.Marshal(automationservice.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{
		ID: "event-1", RuleKey: rule.Key, Rule: rule, ObjectKey: rule.ObjectKey, Operation: rule.Trigger.Operation,
		RecordID: "record-1", RecordVersion: "version-1", Record: recordmodel.Record{ID: "record-1", Data: map[string]any{"status": "ready"}},
		ActorUserID: "user-1", ActorRoleKey: "sales", RequestID: "request-1", IdentityPolicy: "revalidate_initiator",
	}))
	receipt, err := delivery.Accept(t.Context(), integrationsdk.DeliveryRequest{
		MessageID: "message-1", DeduplicationKey: "event-1", WorkspaceID: "workspace-1",
		ConnectorKey: runtimeAutomationConnectorKey, Operation: rule.Key, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID != "message-1" || receipt.InvocationID != "automation:message-1" || receipt.Status != integrationsdk.DeliveryStatusSucceeded {
		t.Fatalf("receipt=%+v", receipt)
	}
	if probe.accepted != 0 {
		t.Fatalf("automation escaped to integration owner: accepted=%d", probe.accepted)
	}

	invalidPayload, _ := json.Marshal(automationservice.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{RuleKey: "missing"}))
	_, err = delivery.Accept(t.Context(), integrationsdk.DeliveryRequest{
		MessageID: "message-2", DeduplicationKey: "event-2", WorkspaceID: "workspace-1",
		ConnectorKey: runtimeAutomationConnectorKey, Operation: "missing", Payload: invalidPayload,
	})
	if err == nil || probe.accepted != 0 {
		t.Fatalf("invalid automation was not returned to the publication retry path: accepted=%d err=%v", probe.accepted, err)
	}
}

func TestRuntimePublicationDeliveryProjectsCompletedAutomationQuery(t *testing.T) {
	delivery := runtimePublicationDelivery{}
	receipt, err := delivery.Query(t.Context(), "automation:message")
	if err != nil || receipt.MessageID != "message" || receipt.Status != integrationsdk.DeliveryStatusSucceeded {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
