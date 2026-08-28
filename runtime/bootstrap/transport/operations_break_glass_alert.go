package transport

import (
	"context"

	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsBreakGlassAuditAlert struct {
	audit *auditapplication.AuditApplicationService
}

func (a operationsBreakGlassAuditAlert) BreakGlassAlert(ctx context.Context, event string, grant operationsmodel.OperationsBreakGlassGrant, principal principalmodel.Principal) error {
	a.audit.AppendWithMetadata(ctx, event, "runtime_break_glass", grant.ID, principal, "Runtime break-glass state changed", nil, map[string]any{"state": grant.State, "expires_at": grant.ExpiresAt, "incident_ref": grant.IncidentRef}, map[string]any{"audit_event_id": grant.AuditEventID, "alert_target": grant.AlertTarget, "approver_ids": grant.ApproverIDs, "revision": grant.Revision})
	return nil
}
