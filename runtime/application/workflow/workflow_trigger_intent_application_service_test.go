package workflow

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestPrepareTriggerIntentsOwnsTransactionalProjection(t *testing.T) {
	workflows := map[string]definitionmodel.WorkflowSchema{"z": {Key: "z", Name: "Z", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}, "a": {Key: "a", Name: "A", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}}
	intents, summaries, err := WorkflowPrepareTriggerIntents(t.Context(), workflows, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}, RequestID: "request-1"}, "record_updated:order")
	if err != nil || len(intents) != 2 || len(summaries) != 2 || intents[0].WorkflowKey != "a" || intents[0].Status != "pending" || intents[0].Result["transactional_intent"] != true || intents[0].Payload["request_id"] != "request-1" {
		t.Fatalf("intents=%#v summaries=%#v err=%v", intents, summaries, err)
	}
}

func TestPrepareTriggerIntentsCancellationFilteringValidationAndPrincipalProjection(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := WorkflowPrepareTriggerIntents(cancelled, nil, "order", recordmodel.Record{}, nil, principalmodel.Principal{}, "record_updated:order"); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	workflows := map[string]definitionmodel.WorkflowSchema{
		"disabled":  {Key: "disabled", Enabled: false},
		"unmatched": {Key: "unmatched", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_created", ObjectKey: "other"}},
	}
	intents, summaries, err := WorkflowPrepareTriggerIntents(t.Context(), workflows, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{}, "record_updated:order")
	if err != nil || len(intents) != 0 || len(summaries) != 0 {
		t.Fatalf("intents=%v summaries=%v err=%v", intents, summaries, err)
	}
	invalid := map[string]definitionmodel.WorkflowSchema{"invalid": {Key: "invalid", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}}}
	if _, _, err := WorkflowPrepareTriggerIntents(t.Context(), invalid, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{}, "record_updated:order"); err == nil {
		t.Fatal("expected graph v2 error")
	}
	invalid["invalid"] = definitionmodel.WorkflowSchema{Key: "invalid", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 1, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
	if _, _, err := WorkflowPrepareTriggerIntents(t.Context(), invalid, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{}, "record_updated:order"); err == nil {
		t.Fatal("expected graph version error")
	}
	invalid["invalid"] = definitionmodel.WorkflowSchema{Key: "invalid", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2}}
	if _, _, err := WorkflowPrepareTriggerIntents(t.Context(), invalid, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{}, "record_updated:order"); err == nil {
		t.Fatal("expected empty graph error")
	}
	valid := map[string]definitionmodel.WorkflowSchema{"valid": {
		Key: "valid", Name: "Valid", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"},
		Graph:           &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}},
	}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user"}, RequestID: "request"}, accessfixture.Bundle{Key: "operator"})
	intents, _, err = WorkflowPrepareTriggerIntents(t.Context(), valid, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principal, "record_updated:order")
	if err != nil || len(intents) != 1 || intents[0].Payload["initiating_user_id"] != "user" || intents[0].Payload["initiating_role_key"] != "operator" {
		t.Fatalf("intents=%v err=%v", intents, err)
	}
	intents, _, err = WorkflowPrepareTriggerIntents(t.Context(), valid, "order", recordmodel.Record{ID: "order-1", Data: map[string]any{}}, nil, principalmodel.Principal{}, "record_updated:order")
	if err != nil || len(intents) != 1 {
		t.Fatalf("minimal intents=%v err=%v", intents, err)
	}
	for _, key := range []string{"request_id", "initiating_user_id", "initiating_role_key"} {
		if _, exists := intents[0].Payload[key]; exists {
			t.Fatalf("unexpected %s in payload=%v", key, intents[0].Payload)
		}
	}
}
