package subjectevidence

import (
	"encoding/json"
	"testing"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	automationstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	operationsstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
)

func TestErasureFencesActualAutomationAndOperationsWriters(t *testing.T) {
	store, handler, ctx := fixture(t)
	automation := automationstore.NewAutomationExecutionStore(store)
	workers := automationstore.NewAutomationWorkerStore(store)
	operations := operationsstore.NewOperationsStore(store)
	now := time.Now().UTC()
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{
		ID: "owned-operation", Kind: "backup.create", Scope: operationsmodel.OperationsScope{WorkspaceID: "workspace-a", ResourceType: "database"},
		RequestedBy: "alice", Reason: "alice@private.example", IdempotencyKey: "alice-backup", RequestFingerprint: "alice-fingerprint",
		Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}}
	if _, _, err := operations.RegisterOperationsCommand(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	grant := operationsmodel.OperationsBreakGlassGrant{ID: "alice-grant", WorkspaceID: "workspace-a", ActorID: "alice", ApproverIDs: []string{"bob", "carol"}, State: operationsmodel.OperationsBreakGlassActive, Reason: "alice@private.example", Revision: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if created, err := operations.CreateOperationsBreakGlass(ctx, grant); err != nil || !created {
		t.Fatalf("initial break glass: %v %v", created, err)
	}
	instruction := automationmodel.AutomationInstructionExecution{WorkspaceID: "workspace-a", ObjectKey: "member_profile", RecordID: "profile-alice", RuleKey: "private", InstructionKey: "notify", IdempotencyKey: "alice-instruction"}
	claimed, ok, err := workers.ClaimInstruction(ctx, "workspace-a", instruction, "worker", now.Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano))
	if err != nil || !ok {
		t.Fatalf("initial instruction: %v %v", ok, err)
	}
	if _, err := workers.CompleteInstruction(ctx, "workspace-a", instruction.IdempotencyKey, claimed.LeaseOwner, claimed.FencingToken, "failed", map[string]any{"email": "alice@private.example"}, "failed", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	plan, err := handler.PrepareSubjectErasure(ctx, "erase-writers", "workspace-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.ErasePreparedSubject(ctx, "erase-writers", "workspace-a", "alice", plan, nil); err != nil {
		t.Fatal(err)
	}
	for _, execution := range []automationmodel.AutomationRuleExecution{
		{ID: "late-actor", ActorID: "alice"},
		{ID: "late-staff", ActorID: "manager", ObjectKey: "member_profile", RecordID: "profile-alice"},
	} {
		if _, err := automation.InsertExecution(ctx, "workspace-a", execution); err == nil {
			t.Fatal("late automation recreated erased evidence", execution.ID)
		}
		if _, err := automation.InsertExecutionSeed(ctx, "workspace-a", execution); err == nil {
			t.Fatal("seed recreated erased evidence", execution.ID)
		}
	}
	if _, ok, err := workers.ClaimInstruction(ctx, "workspace-a", instruction, "late", now.Add(time.Hour).Format(time.RFC3339Nano), now.Add(2*time.Hour).Format(time.RFC3339Nano)); err == nil || ok {
		t.Fatalf("erased instruction claimed: %v %v", ok, err)
	}
	// Restore only stale lease metadata, leaving the permanent fence intact.
	// Matching the old lease must still fail the final SQL write predicate.
	if _, err := store.DB().ExecContext(ctx, `UPDATE _automation_instruction_executions SET status='processing',lease_owner=?,fencing_token=? WHERE workspace_id=? AND id=?`, claimed.LeaseOwner, claimed.FencingToken, "workspace-a", claimed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := workers.CompleteInstruction(ctx, "workspace-a", instruction.IdempotencyKey, claimed.LeaseOwner, claimed.FencingToken, "succeeded", map[string]any{"email": "alice@private.example"}, "", now.Format(time.RFC3339Nano)); err == nil {
		t.Fatal("matching cached lease restored erased result")
	}
	if _, err := workers.HeartbeatInstruction(ctx, "workspace-a", instruction.IdempotencyKey, claimed.LeaseOwner, claimed.FencingToken, now.Add(time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err == nil {
		t.Fatal("erased instruction heartbeat succeeded")
	}
	receipt.Command.Status = operationsmodel.OperationsStatusSucceeded
	receipt.Result = json.RawMessage(`{"email":"alice@private.example"}`)
	if changed, err := operations.UpdateOperationsReceipt(ctx, receipt, operationsmodel.OperationsStatusFailed); err != nil || changed {
		t.Fatalf("cached operations result restored: %v %v", changed, err)
	}
	receipt.Command.ID, receipt.Command.IdempotencyKey = "new-alice-operation", "new-alice-backup"
	if _, _, err := operations.RegisterOperationsCommand(ctx, receipt); err == nil {
		t.Fatal("new operation accepted erased actor")
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE _operation_break_glass_grants SET state='active' WHERE workspace_id='workspace-a' AND id='alice-grant'`); err != nil {
		t.Fatal(err)
	}
	grant.State, grant.RevokedBy, grant.RevocationNote = operationsmodel.OperationsBreakGlassRevoked, "manager", "alice@private.example"
	if changed, err := operations.RevokeOperationsBreakGlass(ctx, grant, 1); err != nil || changed {
		t.Fatalf("cached break glass restored private note: %v %v", changed, err)
	}
	if _, err := store.DB().ExecContext(ctx, `UPDATE _operation_break_glass_grants SET state='revoked' WHERE workspace_id='workspace-a' AND id='alice-grant'`); err != nil {
		t.Fatal(err)
	}
	grant.ID, grant.State = "new-alice-grant", operationsmodel.OperationsBreakGlassActive
	if created, err := operations.CreateOperationsBreakGlass(ctx, grant); err == nil || created {
		t.Fatalf("new grant accepted erased actor: %v %v", created, err)
	}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if _, err := automation.InsertExecution(ctx, workspace, automationmodel.AutomationRuleExecution{ID: "peer", ActorID: "bob", ObjectKey: "member_profile", RecordID: "profile-bob"}); err != nil {
			t.Fatal("peer automation refused", workspace, err)
		}
		peer := receipt
		peer.Command.ID, peer.Command.IdempotencyKey, peer.Command.RequestedBy, peer.Command.Scope.WorkspaceID = "peer-operation-"+workspace, "peer-key", "bob", workspace
		peer.Command.Reason, peer.Result = "bob@private.example", json.RawMessage(`{"email":"bob@private.example"}`)
		if _, _, err := operations.RegisterOperationsCommand(ctx, peer); err != nil {
			t.Fatal("peer operations refused", workspace, err)
		}
	}
	var leaked int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM _automation_instruction_executions WHERE workspace_id='workspace-a' AND result_json LIKE '%alice@private.example%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("private result restored: %d %v", leaked, err)
	}
}
