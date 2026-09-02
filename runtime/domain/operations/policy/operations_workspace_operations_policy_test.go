package policy

import (
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestCrossWorkspaceOperationsRequireKnownPurposeAndAudit(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	for _, purpose := range []operationsmodel.CrossWorkspacePurpose{operationsmodel.CrossWorkspaceReport, operationsmodel.CrossWorkspaceMigration, operationsmodel.CrossWorkspaceSupport} {
		command := operationsmodel.CrossWorkspaceCommand{ID: "op-1", Purpose: purpose, SourceWorkspaceID: "workspace-a", TargetWorkspaceID: "workspace-b", ActorID: "operator", Reason: "incident recovery", Reference: "INC-42", RequestedAt: now}
		if err := OperationsValidateCrossWorkspaceCommand(command, now); err != nil {
			t.Fatalf("purpose %s rejected: %v", purpose, err)
		}
	}
}

func TestCrossWorkspaceOperationsRejectWildcardAndUncontrolledBreakGlass(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	command := operationsmodel.CrossWorkspaceCommand{ID: "op-1", Purpose: operationsmodel.CrossWorkspaceSupport, SourceWorkspaceID: "workspace-a", TargetWorkspaceID: "workspace-b", ActorID: "operator", Reason: "incident", Reference: "INC-42", RequestedAt: now}
	command.TargetWorkspaceID = "*"
	if err := OperationsValidateCrossWorkspaceCommand(command, now); err == nil {
		t.Fatal("workspace wildcard accepted")
	}
	command.TargetWorkspaceID = "workspace-b"
	command.BreakGlass = &operationsmodel.BreakGlassGrant{ExpiresAt: now.Add(30 * time.Minute), ApproverIDs: []string{"approver-a"}, AuditEventID: "audit-1"}
	if err := OperationsValidateCrossWorkspaceCommand(command, now); err == nil {
		t.Fatal("single-approval break-glass accepted")
	}
	command.BreakGlass.ApproverIDs = []string{"approver-a", "approver-b"}
	if err := OperationsValidateCrossWorkspaceCommand(command, now); err != nil {
		t.Fatalf("controlled break-glass rejected: %v", err)
	}
}

func TestWorkspaceDeletionRequiresFreezeExportRetentionAndCleanup(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	base := operationsmodel.WorkspaceDeletion{WorkspaceID: "workspace-a", ActorID: "operator", Reason: "tenant closure", Reference: "REQ-42", UpdatedAt: now}
	current := base
	current.State = operationsmodel.WorkspaceDeletionActive
	next := base
	next.State = operationsmodel.WorkspaceDeletionFrozen
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
	current, next = next, base
	next.State, next.ExportReference = operationsmodel.WorkspaceDeletionExported, "export://workspace-a/42"
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
	current, next = next, base
	next.State, next.RetentionUntil = operationsmodel.WorkspaceDeletionRetained, now.Add(24*time.Hour)
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
	current = next
	next = base
	next.State = operationsmodel.WorkspaceDeletionCleanupPending
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err == nil {
		t.Fatal("cleanup before retention accepted")
	}
	current.RetentionUntil = now.Add(-time.Second)
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
	current, next = next, base
	next.State, next.CleanupEvidence = operationsmodel.WorkspaceDeletionDeleted, "purge-report-42"
	if err := OperationsTransitionWorkspaceDeletion(current, next, now); err != nil {
		t.Fatal(err)
	}
}
