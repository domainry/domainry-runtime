package operationsmodel

import "time"

type OperationsBreakGlassState string

const (
	OperationsBreakGlassActive  OperationsBreakGlassState = "active"
	OperationsBreakGlassRevoked OperationsBreakGlassState = "revoked"
	OperationsBreakGlassExpired OperationsBreakGlassState = "expired"
)

type OperationsBreakGlassGrant struct {
	ID             string                    `json:"id"`
	WorkspaceID    string                    `json:"workspace_id"`
	State          OperationsBreakGlassState `json:"state"`
	ActorID        string                    `json:"actor_id"`
	ApproverIDs    []string                  `json:"approver_ids"`
	Reason         string                    `json:"reason"`
	IncidentRef    string                    `json:"incident_ref"`
	AlertTarget    string                    `json:"alert_target"`
	AuditEventID   string                    `json:"audit_event_id"`
	ExpiresAt      time.Time                 `json:"expires_at"`
	Revision       int64                     `json:"revision"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
	RevokedAt      *time.Time                `json:"revoked_at,omitempty"`
	RevokedBy      string                    `json:"revoked_by,omitempty"`
	RevocationNote string                    `json:"revocation_note,omitempty"`
}

func (g OperationsBreakGlassGrant) Active(now time.Time) bool {
	return g.State == OperationsBreakGlassActive && g.ExpiresAt.After(now)
}
