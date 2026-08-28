package integration

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"
)

func TestPlanEventMappingExecutionNormalizesActionTarget(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{
		Key: "action", Provider: "crm", EventType: "updated", TargetType: "action",
		ObjectKey: "invoice", RecordIDPath: "target.id", ActionKey: "approve",
		ActionInput: map[string]string{"reason": "payload.reason", "count": "payload.count"},
		EventFields: []integrationmodel.IntegrationEventFieldSchema{
			{Path: "target.id", Type: "relation"},
			{Path: "payload.reason", Type: "text"},
			{Path: "payload.count", Type: "integer"},
		},
	}}})})
	plan, handled, err := service.PlanEventMappingExecution(integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated", Payload: map[string]any{
		"target":  map[string]any{"id": "invoice-1"},
		"payload": map[string]any{"reason": "verified", "count": 2},
		"ignored": true,
	}})
	if err != nil || !handled || plan.TargetType != "action" || plan.ObjectKey != "invoice" || plan.RecordID != "invoice-1" || plan.ActionKey != "approve" {
		t.Fatalf("plan=%#v handled=%v err=%v", plan, handled, err)
	}
	if plan.Payload["reason"] != "verified" || plan.Payload["count"] != 2 || plan.Payload["ignored"] != nil {
		t.Fatalf("action input projection=%#v", plan.Payload)
	}
	missing := integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated", Payload: map[string]any{
		"target": map[string]any{"id": "invoice-1"},
	}}
	if plan, handled, err := service.PlanEventMappingExecution(missing); err != nil || !handled || len(plan.Payload) != 0 {
		t.Fatalf("missing action input plan=%#v handled=%v err=%v", plan, handled, err)
	}
	wrongType := integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated", Payload: map[string]any{
		"target": map[string]any{"id": "invoice-1"}, "payload": map[string]any{"count": "many"},
	}}
	if _, handled, err := service.PlanEventMappingExecution(wrongType); !handled || testErrorCode(err) != "backend.integration.event_mapping.event_contract_invalid" {
		t.Fatalf("wrong type handled=%v err=%v", handled, err)
	}
}

func TestPlanEventMappingExecutionRejectsIncompleteTarget(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "workflow", Provider: "crm", EventType: "updated", TargetType: "workflow"}}})})
	_, handled, err := service.PlanEventMappingExecution(integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated"})
	if !handled || testErrorCode(err) != "backend.integration.event_mapping.missing_workflow" {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestPlanEventMappingExecutionRejectsIncompleteActionTarget(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "action", Provider: "crm", EventType: "updated", TargetType: "action"}}})})
	_, handled, err := service.PlanEventMappingExecution(integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated"})
	if !handled || testErrorCode(err) != "backend.integration.event_mapping.missing_action_target" {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	for _, mapping := range []integrationmodel.IntegrationEventMappingSchema{
		{Key: "action", Provider: "crm", EventType: "updated", TargetType: "action", ObjectKey: "customer"},
		{Key: "action", Provider: "crm", EventType: "updated", TargetType: "action", ObjectKey: "customer", RecordID: "record"},
	} {
		service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{mapping}})})
		if _, handled, err := service.PlanEventMappingExecution(integrationmodel.IntegrationEvent{Provider: "crm", EventType: "updated"}); !handled || testErrorCode(err) != "backend.integration.event_mapping.missing_action_target" {
			t.Fatalf("mapping=%#v handled=%v err=%v", mapping, handled, err)
		}
	}
}

func TestIntegrationEventMappingSelectionAndPayloadEdges(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{
		{Key: "", Provider: "provider"},
		{Key: "other-provider", Provider: "other"},
		{Key: "other-event", Provider: "provider", EventType: "deleted"},
		{Key: "other-command", Provider: "provider", EventType: "created", CommandPrefix: "other:"},
		{Key: "match", Provider: "provider", EventType: "created", CommandPrefix: "run:", TargetType: "owner_task", ExternalIdentity: integrationmodel.IntegrationExternalIdentityMappingSchema{SubjectPath: "actor.id", SubjectType: "device", NamePath: "actor.name"}, Payload: map[string]any{"overlay": true}},
	}})
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	event := integrationmodel.IntegrationEvent{ID: "event", Provider: "provider", EventType: "created", ExternalID: "external", Payload: map[string]any{"command": "run:task", "actor": map[string]any{"id": "subject", "name": "Name"}}}
	mapping, ok := service.EventMappingForEvent(event)
	if !ok || mapping.Key != "match" {
		t.Fatalf("mapping=%#v ok=%v", mapping, ok)
	}
	event.Payload["command"] = "none"
	if _, ok := service.EventMappingForEvent(event); ok {
		t.Fatal("unexpected command mapping")
	}
	event.Payload["command"] = "run:task"
	identity := ExternalIdentityFromEventMapping(mapping, event)
	if identity.Provider != "provider" || identity.ExternalSubject != "subject" || identity.ExternalSubjectType != "device" || identity.ExternalName != "Name" || identity.OnUnmapped != "reject" {
		t.Fatalf("identity=%#v", identity)
	}
	mapping.ExternalIdentity.Provider, mapping.ExternalIdentity.OnUnmapped = "identity-provider", "read_only"
	identity = ExternalIdentityFromEventMapping(mapping, event)
	if identity.Provider != "identity-provider" || identity.OnUnmapped != "read_only" {
		t.Fatalf("explicit identity=%#v", identity)
	}
	mapping.Provider, mapping.ExternalIdentity.Provider = "", ""
	identity = ExternalIdentityFromEventMapping(mapping, event)
	if identity.Provider != "provider" {
		t.Fatalf("event provider identity=%#v", identity)
	}
	payload := EventMappingPayload(mapping, integrationmodel.IntegrationEvent{ID: "event", Provider: "provider", EventType: "created", ExternalID: "external"})
	if payload["overlay"] != true || payload["integration_event_id"] != "event" || payload["integration_external_id"] != "external" {
		t.Fatalf("payload=%#v", payload)
	}
	mapping.Payload["connection_key"] = "internal"
	payload = EventMappingPayload(mapping, event)
	if _, exists := payload["connection_key"]; exists {
		t.Fatalf("routing key leaked: %#v", payload)
	}
	if got := eventRoutingConnection(map[string]any{eventRoutingConnectionKey: "", "_integration_context": map[string]any{"connection_key": "fallback"}}); got != "fallback" {
		t.Fatalf("fallback connection=%q", got)
	}
	if MappingValue(" static ", event.Payload, "actor.id") != "static" || MappingValue("", event.Payload, "actor.id") != "subject" {
		t.Fatal("mapping value normalization mismatch")
	}
	for _, path := range []string{"", "actor..id", "actor.id.value", "missing", "nil"} {
		payload := map[string]any{"actor": map[string]any{"id": "subject"}, "nil": nil}
		if got := PayloadPathString(payload, path); got != "" {
			t.Fatalf("path %q=%q", path, got)
		}
	}
	if PayloadPathString(event.Payload, "actor.id") != "subject" {
		t.Fatal("valid payload path missing")
	}
	catchAll := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "all", Provider: "provider"}}})})
	if mapping, ok := catchAll.EventMappingForEvent(integrationmodel.IntegrationEvent{Provider: "provider", EventType: "anything"}); !ok || mapping.Key != "all" {
		t.Fatalf("catch-all mapping=%#v ok=%v", mapping, ok)
	}
}

func TestEventMappingConnectionPinSelectsMatchingConnectionAndStaysOutOfActionInput(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{
		{Key: "secondary", Provider: "provider", EventType: "created", TargetType: "action", ObjectKey: "invoice", RecordID: "invoice-1", ActionKey: "approve", ActionInput: map[string]string{"reason": "reason"}, EventFields: []integrationmodel.IntegrationEventFieldSchema{{Path: "reason", Type: "text"}}, Payload: map[string]any{"connection_key": "secondary", "fixed": true}},
		{Key: "primary", Provider: "provider", EventType: "created", TargetType: "action", ObjectKey: "invoice", RecordID: "invoice-1", ActionKey: "approve", ActionInput: map[string]string{"reason": "reason"}, EventFields: []integrationmodel.IntegrationEventFieldSchema{{Path: "reason", Type: "text"}}, Payload: map[string]any{"connection_key": "primary", "fixed": true}},
	}})
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	event := integrationmodel.IntegrationEvent{Provider: "provider", EventType: "created", Payload: map[string]any{
		"reason": "verified", eventRoutingConnectionKey: "primary",
	}}
	plan, handled, err := service.PlanEventMappingExecution(event)
	if err != nil || !handled || plan.Mapping.Key != "primary" {
		t.Fatalf("plan=%#v handled=%v err=%v", plan, handled, err)
	}
	if plan.Payload["reason"] != "verified" || plan.Payload["fixed"] != true {
		t.Fatalf("action input projection=%#v", plan.Payload)
	}
	if _, exists := plan.Payload["connection_key"]; exists {
		t.Fatalf("connection routing metadata leaked into action input: %#v", plan.Payload)
	}
}
