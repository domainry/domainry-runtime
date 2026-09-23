package transport

import (
	"context"
	"fmt"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/requestcontext"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsBreakGlassAuditAlert struct {
	audit *auditapplication.AuditApplicationService
}

func (a operationsBreakGlassAuditAlert) BreakGlassAlert(ctx context.Context, event string, grant operationsmodel.OperationsBreakGlassGrant, principal principalmodel.Principal) error {
	if a.audit == nil {
		return fmt.Errorf("Runtime break-glass Audit appender is unavailable")
	}
	operationID := requestcontext.OwnerExecutionID(ctx)
	if operationID == "" {
		return fmt.Errorf("Runtime break-glass operation identity is required")
	}
	request := auditapplication.AuditAppendRequest{
		IdempotencyKey: operationsapplication.BreakGlassAuditIdempotencyKey(event, operationID),
		Family:         auditmodel.EventFamilyRuntimeOperations,
		Event:          event,
		ObjectKey:      "runtime_break_glass",
		RecordID:       grant.ID,
		Principal:      principal,
		Summary:        "Runtime break-glass state changed",
		After:          map[string]any{"state": grant.State, "expires_at": grant.ExpiresAt, "incident_ref": grant.IncidentRef},
		Metadata:       map[string]any{"alert_target": grant.AlertTarget, "approver_ids": grant.ApproverIDs, "revision": grant.Revision},
	}
	prepared := a.audit.NewAuditEvent(ctx, request)
	if prepared.ID == "" || strings.TrimSpace(prepared.ID) != strings.TrimSpace(grant.AuditEventID) {
		return fmt.Errorf("Runtime break-glass Audit identity mismatch")
	}
	return a.audit.AppendAudit(ctx, request)
}
