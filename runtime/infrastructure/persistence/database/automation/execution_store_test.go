package automation

import (
	"context"
	"errors"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

type contextAutomationExecutionContract interface {
	InsertExecution(context.Context, string, automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error)
	ListExecutions(context.Context, string, automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error)
}

var _ contextAutomationExecutionContract = AutomationExecutionStore{}

func TestAutomationExecutionStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationExecutionStore(store)
	value, err := repository.InsertExecution(t.Context(), "workspace-primary", automationmodel.AutomationRuleExecution{WorkspaceID: "workspace-primary", RuleKey: "notify", ObjectKey: "customer", RecordID: "c-1", Phase: "after", Operation: "update", Status: "succeeded", Trace: map[string]any{"connector_key": "webhook"}})
	if err != nil {
		t.Fatalf("insert execution: %v", err)
	}
	values, err := repository.ListExecutions(t.Context(), "workspace-primary", automationmodel.AutomationExecutionFilter{RuleKey: "notify", ConnectorKey: "webhook", Limit: 10})
	if err != nil || len(values) != 1 || values[0].ID != value.ID {
		t.Fatalf("list executions=%#v err=%v", values, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListExecutions(cancelled, "workspace-primary", automationmodel.AutomationExecutionFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, err := repository.InsertExecution(cancelled, "workspace-primary", automationmodel.AutomationRuleExecution{RuleKey: "never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled insert error=%v", err)
	}
	if _, err := repository.InsertExecutionSeed(cancelled, "workspace-primary", automationmodel.AutomationRuleExecution{ID: "never", RuleKey: "never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled seed insert error=%v", err)
	}
}

func TestAutomationStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	executions := NewAutomationExecutionStore(store)
	for _, value := range []automationmodel.AutomationRuleExecution{
		{ID: "execution-a", WorkspaceID: "workspace-a", RuleKey: "shared-rule", ObjectKey: "order", RecordID: "shared-record", Phase: "after", Operation: "update", Status: "succeeded"},
		{ID: "execution-b", WorkspaceID: "workspace-b", RuleKey: "shared-rule", ObjectKey: "order", RecordID: "shared-record", Phase: "after", Operation: "update", Status: "failed"},
	} {
		if _, err := executions.InsertExecution(t.Context(), value.WorkspaceID, value); err != nil {
			t.Fatalf("insert %s: %v", value.ID, err)
		}
	}
	valuesA, err := executions.ListExecutions(t.Context(), "workspace-a", automationmodel.AutomationExecutionFilter{RuleKey: "shared-rule"})
	if err != nil || len(valuesA) != 1 || valuesA[0].ID != "execution-a" {
		t.Fatalf("workspace A executions=%#v err=%v", valuesA, err)
	}
	valuesB, err := executions.ListExecutions(t.Context(), "workspace-b", automationmodel.AutomationExecutionFilter{RuleKey: "shared-rule"})
	if err != nil || len(valuesB) != 1 || valuesB[0].ID != "execution-b" {
		t.Fatalf("workspace B executions=%#v err=%v", valuesB, err)
	}
	if _, err := executions.InsertExecution(t.Context(), "workspace-b", automationmodel.AutomationRuleExecution{WorkspaceID: "workspace-a"}); err == nil {
		t.Fatal("mismatched execution workspace was accepted")
	}
	if _, err := executions.ListExecutions(t.Context(), "", automationmodel.AutomationExecutionFilter{}); err == nil {
		t.Fatal("missing execution workspace was accepted")
	}
	if _, err := executions.InsertExecution(t.Context(), "", automationmodel.AutomationRuleExecution{}); err == nil {
		t.Fatal("missing execution insert workspace was accepted")
	}
	if _, err := executions.InsertExecutionSeed(t.Context(), "", automationmodel.AutomationRuleExecution{ID: "seed"}); err == nil {
		t.Fatal("missing execution seed workspace was accepted")
	}
	for _, workspaceID := range []string{"workspace-a", "workspace-b", "workspace-a"} {
		if _, err := executions.InsertExecutionSeed(t.Context(), workspaceID, automationmodel.AutomationRuleExecution{ID: "shared-seed", WorkspaceID: workspaceID, RuleKey: "seed-rule"}); err != nil {
			t.Fatalf("insert workspace-scoped seed for %s: %v", workspaceID, err)
		}
	}
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		seeded, err := executions.ListExecutions(t.Context(), workspaceID, automationmodel.AutomationExecutionFilter{RuleKey: "seed-rule"})
		if err != nil || len(seeded) != 1 || seeded[0].ID != "shared-seed" {
			t.Fatalf("workspace-scoped seed for %s=%#v err=%v", workspaceID, seeded, err)
		}
	}

	workers := NewAutomationWorkerStore(store)
	requestA := automationmodel.AutomationInstructionExecution{WorkspaceID: "workspace-a", IdempotencyKey: "shared-key", RuleKey: "shared-rule", ObjectKey: "order", RecordID: "shared-record", RecordVersion: "v1", Operation: "update", InstructionKey: "notify"}
	requestB := requestA
	requestB.WorkspaceID = "workspace-b"
	claimA, claimed, err := workers.ClaimInstruction(t.Context(), "workspace-a", requestA, "worker-a", "2026-07-19T00:00:00Z", "2026-07-19T00:05:00Z")
	if err != nil || !claimed {
		t.Fatalf("claim workspace A: claimed=%v err=%v", claimed, err)
	}
	claimB, claimed, err := workers.ClaimInstruction(t.Context(), "workspace-b", requestB, "worker-b", "2026-07-19T00:00:00Z", "2026-07-19T00:05:00Z")
	if err != nil || !claimed {
		t.Fatalf("claim workspace B: claimed=%v err=%v", claimed, err)
	}
	if claimA.ID == claimB.ID {
		t.Fatalf("workspace-scoped claims reused id %q", claimA.ID)
	}
	if _, err := workers.HeartbeatInstruction(t.Context(), "workspace-b", requestB.IdempotencyKey, claimA.LeaseOwner, claimA.FencingToken, "2026-07-19T00:06:00Z", "2026-07-19T00:01:00Z"); err == nil {
		t.Fatal("workspace B heartbeated workspace A lease")
	}
	if _, err := workers.CompleteInstruction(t.Context(), "workspace-b", requestB.IdempotencyKey, claimA.LeaseOwner, claimA.FencingToken, "succeeded", nil, "", "2026-07-19T00:01:00Z"); err == nil {
		t.Fatal("workspace B completed workspace A lease")
	}
	currentA, found, err := workers.find(t.Context(), "workspace-a", requestA.IdempotencyKey)
	if err != nil || !found || currentA.Status != "processing" || currentA.LeaseOwner != claimA.LeaseOwner {
		t.Fatalf("workspace A lease changed by workspace B: %#v found=%v err=%v", currentA, found, err)
	}
	if _, err := workers.CompleteInstruction(t.Context(), "workspace-a", requestA.IdempotencyKey, claimA.LeaseOwner, claimA.FencingToken, "succeeded", map[string]any{"workspace": "a"}, "", "2026-07-19T00:01:00Z"); err != nil {
		t.Fatalf("complete workspace A: %v", err)
	}
	currentB, found, err := workers.find(t.Context(), "workspace-b", requestB.IdempotencyKey)
	if err != nil || !found || currentB.Status != "processing" || currentB.LeaseOwner != claimB.LeaseOwner {
		t.Fatalf("workspace B lease changed by workspace A completion: %#v found=%v err=%v", currentB, found, err)
	}
	if _, _, err := workers.ClaimInstruction(t.Context(), "", automationmodel.AutomationInstructionExecution{}, "worker-a", "", ""); err == nil {
		t.Fatal("missing claim workspace was accepted")
	}
	if _, _, err := workers.ClaimInstruction(t.Context(), "workspace-b", requestA, "worker-b", "", ""); err == nil {
		t.Fatal("mismatched claim workspace was accepted")
	}
	if _, err := workers.HeartbeatInstruction(t.Context(), "", requestA.IdempotencyKey, claimA.LeaseOwner, claimA.FencingToken, "", ""); err == nil {
		t.Fatal("missing heartbeat workspace was accepted")
	}
	if _, err := workers.CompleteInstruction(t.Context(), "", requestA.IdempotencyKey, claimA.LeaseOwner, claimA.FencingToken, "succeeded", nil, "", ""); err == nil {
		t.Fatal("missing completion workspace was accepted")
	}
}
