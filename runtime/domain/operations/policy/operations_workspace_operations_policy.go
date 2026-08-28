package policy

import (
	"fmt"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

const MaximumBreakGlassDuration = time.Hour

var crossWorkspacePermissions = map[operationsmodel.CrossWorkspacePurpose]string{
	operationsmodel.CrossWorkspaceReport:    "runtime.cross_workspace.report",
	operationsmodel.CrossWorkspaceMigration: "runtime.cross_workspace.migration",
	operationsmodel.CrossWorkspaceSupport:   "runtime.cross_workspace.support",
}

func OperationsValidateCrossWorkspaceCommand(command operationsmodel.CrossWorkspaceCommand, now time.Time) error {
	command.ID = strings.TrimSpace(command.ID)
	command.Permission = strings.TrimSpace(command.Permission)
	command.SourceWorkspaceID = strings.TrimSpace(command.SourceWorkspaceID)
	command.TargetWorkspaceID = strings.TrimSpace(command.TargetWorkspaceID)
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.Reason = strings.TrimSpace(command.Reason)
	command.Reference = strings.TrimSpace(command.Reference)
	expectedPermission, known := crossWorkspacePermissions[command.Purpose]
	if !known || command.Permission != expectedPermission {
		return fmt.Errorf("operations.cross_workspace_permission_required")
	}
	if command.ID == "" || command.SourceWorkspaceID == "" || command.TargetWorkspaceID == "" || command.SourceWorkspaceID == command.TargetWorkspaceID {
		return fmt.Errorf("operations.cross_workspace_scope_required")
	}
	if command.SourceWorkspaceID == "*" || command.TargetWorkspaceID == "*" {
		return fmt.Errorf("operations.workspace_wildcard_forbidden")
	}
	if command.ActorID == "" || command.Reason == "" || command.Reference == "" || command.RequestedAt.IsZero() {
		return fmt.Errorf("operations.cross_workspace_audit_context_required")
	}
	if command.BreakGlass != nil {
		if err := OperationsValidateBreakGlass(*command.BreakGlass, command.ActorID, now); err != nil {
			return err
		}
	}
	return nil
}

func OperationsValidateBreakGlass(grant operationsmodel.BreakGlassGrant, actorID string, now time.Time) error {
	if strings.TrimSpace(grant.AuditEventID) == "" || grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(now) || grant.ExpiresAt.After(now.Add(MaximumBreakGlassDuration)) {
		return fmt.Errorf("operations.break_glass_time_or_audit_required")
	}
	approvers := map[string]struct{}{}
	for _, approverID := range grant.ApproverIDs {
		approverID = strings.TrimSpace(approverID)
		if approverID != "" && approverID != strings.TrimSpace(actorID) {
			approvers[approverID] = struct{}{}
		}
	}
	if len(approvers) < 2 {
		return fmt.Errorf("operations.break_glass_dual_approval_required")
	}
	return nil
}

func OperationsTransitionWorkspaceDeletion(current operationsmodel.WorkspaceDeletion, next operationsmodel.WorkspaceDeletion, now time.Time) error {
	current.WorkspaceID, next.WorkspaceID = strings.TrimSpace(current.WorkspaceID), strings.TrimSpace(next.WorkspaceID)
	if current.WorkspaceID == "" || next.WorkspaceID != current.WorkspaceID || strings.TrimSpace(next.ActorID) == "" || strings.TrimSpace(next.Reason) == "" || strings.TrimSpace(next.Reference) == "" || next.UpdatedAt.IsZero() {
		return fmt.Errorf("operations.workspace_deletion_audit_context_required")
	}
	switch {
	case current.State == operationsmodel.WorkspaceDeletionActive && next.State == operationsmodel.WorkspaceDeletionFrozen:
		return nil
	case current.State == operationsmodel.WorkspaceDeletionFrozen && next.State == operationsmodel.WorkspaceDeletionExported:
		if strings.TrimSpace(next.ExportReference) == "" {
			return fmt.Errorf("operations.workspace_export_evidence_required")
		}
		return nil
	case current.State == operationsmodel.WorkspaceDeletionExported && next.State == operationsmodel.WorkspaceDeletionRetained:
		if next.RetentionUntil.IsZero() || !next.RetentionUntil.After(now) {
			return fmt.Errorf("operations.workspace_retention_window_required")
		}
		return nil
	case current.State == operationsmodel.WorkspaceDeletionRetained && next.State == operationsmodel.WorkspaceDeletionCleanupPending:
		if current.LegalHold || current.RetentionUntil.IsZero() || now.Before(current.RetentionUntil) {
			return fmt.Errorf("operations.workspace_cleanup_blocked_by_retention")
		}
		return nil
	case current.State == operationsmodel.WorkspaceDeletionCleanupPending && next.State == operationsmodel.WorkspaceDeletionDeleted:
		if strings.TrimSpace(next.CleanupEvidence) == "" {
			return fmt.Errorf("operations.workspace_cleanup_evidence_required")
		}
		return nil
	default:
		return fmt.Errorf("operations.workspace_deletion_transition_invalid")
	}
}
