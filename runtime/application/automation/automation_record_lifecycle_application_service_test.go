package automation

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationdomain "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestAutomationAfterOutboxUsesStableRequiredDedupIdentity(t *testing.T) {
	rules := []automationmodel.AutomationRuleSchema{{
		Key: "proposal_after_create", ObjectKey: "proposal", Enabled: true,
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "audit", Type: "emit_event"}},
	}}
	record := recordmodel.Record{ID: "proposal-1", Data: map[string]any{"status": "pending"}, CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "operator"}, RequestID: "request-1"}, accessfixture.Bundle{Key: "operator"})
	first := AutomationAfterOutbox(rules, "proposal", "create", nil, record, principal, "default")
	second := AutomationAfterOutbox(rules, "proposal", "create", nil, record, principal, "default")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("outbox counts=%d/%d, want 1/1", len(first), len(second))
	}
	if first[0].DedupKey == "" || first[0].DedupKey != first[0].ID || first[0].DedupKey != second[0].DedupKey {
		t.Fatalf("automation outbox identity is not stable: first=%#v second=%#v", first[0], second[0])
	}
}

func TestAutomationAfterOutboxPersistsIdentityAndCausationChain(t *testing.T) {
	rules := []automationmodel.AutomationRuleSchema{{Key: "after", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}, RequestID: "request", CorrelationID: "root", CausationID: "action", AutomationDepth: 2, VisitedRuleKeys: []string{"first"}}, accessfixture.Bundle{Key: "operator"})
	messages := AutomationAfterOutbox(rules, "order", "update", nil, recordmodel.Record{ID: "record", UpdatedAt: "v2", Data: map[string]any{"status": "ready"}}, principal, "workspace")
	if len(messages) != 1 {
		t.Fatalf("messages=%#v", messages)
	}
	event := automationdomain.LifecycleEventFromPayload(messages[0].Payload)
	if event.IdentityPolicy != "revalidate_initiator" || event.CorrelationID != "root" || event.CausationID != "action" || event.AutomationDepth != 2 || len(event.VisitedRuleKeys) != 1 || event.VisitedRuleKeys[0] != "first" {
		t.Fatalf("event=%#v", event)
	}
}
