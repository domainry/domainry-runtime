package automation

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestAutomationRunsKeepRuleHistoryAndInstructionLeaseStateSeparatedByKind(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	workspaceID := "workspace-primary"
	ruleID := "shared-business-key"
	rule, err := NewAutomationExecutionStore(store).InsertExecution(t.Context(), workspaceID, automationmodel.AutomationRuleExecution{
		ID: ruleID, RuleKey: "sync-customer", ObjectKey: "customer", RecordID: "customer-1",
		Phase: "after", Operation: "update", Status: "succeeded",
		Candidate: map[string]any{"name": "updated"}, Trace: map[string]any{"connector_key": "crm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID != ruleID {
		t.Fatalf("rule id=%q want=%q", rule.ID, ruleID)
	}

	instruction, claimed, err := NewAutomationWorkerStore(store).ClaimInstruction(t.Context(), workspaceID, automationmodel.AutomationInstructionExecution{
		IdempotencyKey: ruleID, RuleKey: "sync-customer", ObjectKey: "customer", RecordID: "customer-1",
		RecordVersion: "v1", Operation: "update", InstructionKey: "notify",
	}, "worker-a", "2026-01-01T00:00:00Z", "2026-01-01T00:03:00Z")
	if err != nil || !claimed {
		t.Fatalf("instruction claim: claimed=%v err=%v", claimed, err)
	}
	if instruction.FencingToken != 1 {
		t.Fatalf("instruction fencing token=%d want=1", instruction.FencingToken)
	}

	rules, err := NewAutomationExecutionStore(store).ListExecutions(t.Context(), workspaceID, automationmodel.AutomationExecutionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != ruleID {
		t.Fatalf("rule history leaked instruction rows: %#v", rules)
	}

	rows, err := store.DB().QueryContext(t.Context(), "SELECT "+store.Identifier("run_kind")+", COUNT(*) FROM "+store.TableIdentifier(automationRunsTable)+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1)+" GROUP BY "+store.Identifier("run_kind")+" ORDER BY "+store.Identifier("run_kind"), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]int{"instruction": 1, "rule": 1}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		if want[kind] != count {
			t.Fatalf("run kind %q count=%d want=%d", kind, count, want[kind])
		}
		delete(want, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(want) != 0 {
		t.Fatalf("missing run kinds: %#v", want)
	}
}
