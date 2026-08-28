package service

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestLifecycleEventRoundTripPreservesRecordEvidence(t *testing.T) {
	event := automationmodel.AutomationLifecycleEvent{ID: "event-1", RuleKey: "rule-1", ObjectKey: "customer", Operation: "update", RecordID: "record-1", RecordVersion: "v1", Before: map[string]any{"status": "new"}, Record: recordmodel.Record{ID: "record-1", Data: map[string]any{"status": "active"}}, ActorUserID: "user-1", CorrelationID: "correlation-1", CausationID: "cause-1", IdentityPolicy: "revalidate_initiator", AutomationDepth: 2, VisitedRuleKeys: []string{"prior"}}
	decoded := LifecycleEventFromPayload(LifecycleEventPayload(event))
	if decoded.ID != event.ID || decoded.RuleKey != event.RuleKey || decoded.Record.ID != event.Record.ID || decoded.Record.Data["status"] != "active" || decoded.Before["status"] != "new" {
		t.Fatalf("decoded=%#v", decoded)
	}
	if decoded.CorrelationID != "correlation-1" || decoded.CausationID != "cause-1" || decoded.IdentityPolicy != "revalidate_initiator" || decoded.AutomationDepth != 2 || len(decoded.VisitedRuleKeys) != 1 || decoded.VisitedRuleKeys[0] != "prior" {
		t.Fatalf("chain evidence=%#v", decoded)
	}
}
