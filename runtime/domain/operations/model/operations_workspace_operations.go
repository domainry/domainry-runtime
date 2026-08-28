package operationsmodel

import "time"

type CrossWorkspacePurpose string

const (
	CrossWorkspaceReport    CrossWorkspacePurpose = "report"
	CrossWorkspaceMigration CrossWorkspacePurpose = "migration"
	CrossWorkspaceSupport   CrossWorkspacePurpose = "support"
)

type CrossWorkspaceCommand struct {
	ID                string                `json:"id"`
	Purpose           CrossWorkspacePurpose `json:"purpose"`
	Permission        string                `json:"permission"`
	SourceWorkspaceID string                `json:"source_workspace_id"`
	TargetWorkspaceID string                `json:"target_workspace_id"`
	ActorID           string                `json:"actor_id"`
	Reason            string                `json:"reason"`
	Reference         string                `json:"reference"`
	RequestedAt       time.Time             `json:"requested_at"`
	BreakGlass        *BreakGlassGrant      `json:"break_glass,omitempty"`
}

type BreakGlassGrant struct {
	ExpiresAt    time.Time `json:"expires_at"`
	ApproverIDs  []string  `json:"approver_ids"`
	AuditEventID string    `json:"audit_event_id"`
}

type WorkspaceDeletionState string

const (
	WorkspaceDeletionActive         WorkspaceDeletionState = "active"
	WorkspaceDeletionFrozen         WorkspaceDeletionState = "frozen"
	WorkspaceDeletionExported       WorkspaceDeletionState = "exported"
	WorkspaceDeletionRetained       WorkspaceDeletionState = "retained"
	WorkspaceDeletionCleanupPending WorkspaceDeletionState = "cleanup_pending"
	WorkspaceDeletionDeleted        WorkspaceDeletionState = "deleted"
)

type WorkspaceDeletion struct {
	WorkspaceID     string                 `json:"workspace_id"`
	State           WorkspaceDeletionState `json:"state"`
	ActorID         string                 `json:"actor_id"`
	Reason          string                 `json:"reason"`
	Reference       string                 `json:"reference"`
	ExportReference string                 `json:"export_reference,omitempty"`
	RetentionUntil  time.Time              `json:"retention_until,omitempty"`
	LegalHold       bool                   `json:"legal_hold"`
	CleanupEvidence string                 `json:"cleanup_evidence,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at"`
}
