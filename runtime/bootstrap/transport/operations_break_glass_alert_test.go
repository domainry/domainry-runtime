package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type breakGlassAuditRepository struct {
	workspaceID string
	event       auditmodel.AuditEvent
	err         error
}

func (r *breakGlassAuditRepository) InsertAuditEvent(_ context.Context, workspaceID string, event auditmodel.AuditEvent) error {
	r.workspaceID, r.event = workspaceID, event
	return r.err
}

func (*breakGlassAuditRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}

func (*breakGlassAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}

func (*breakGlassAuditRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, nil
}

func TestOperationsBreakGlassAlertAppendsAuditEvidence(t *testing.T) {
	repository := &breakGlassAuditRepository{}
	alert := operationsBreakGlassAuditAlert{audit: auditapplication.NewAuditApplicationService(repository)}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}
	grant := operationsmodel.OperationsBreakGlassGrant{
		ID: "grant-1", State: operationsmodel.OperationsBreakGlassActive, ExpiresAt: time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC),
		IncidentRef: "INC-1", AlertTarget: "security", ApproverIDs: []string{"a", "b"}, Revision: 3,
	}
	ctx := requestcontext.WithOwnerExecutionID(t.Context(), "operation-1")
	grant.AuditEventID = auditmodel.IdempotentEventID("workspace-a", operationsapplication.BreakGlassAuditIdempotencyKey("runtime_break_glass_enabled", "operation-1"))
	if err := alert.BreakGlassAlert(ctx, "runtime_break_glass_enabled", grant, principal); err != nil {
		t.Fatal(err)
	}
	if repository.workspaceID != "workspace-a" || repository.event.Event != "runtime_break_glass_enabled" || repository.event.RecordID != grant.ID {
		t.Fatalf("workspace=%q event=%#v", repository.workspaceID, repository.event)
	}
	if repository.event.ID != grant.AuditEventID || repository.event.OperationID != "operation-1" || repository.event.After["state"] != grant.State || repository.event.Metadata["revision"] != grant.Revision {
		t.Fatalf("after=%#v metadata=%#v", repository.event.After, repository.event.Metadata)
	}
	repository.err = errors.New("audit unavailable")
	if err := alert.BreakGlassAlert(ctx, "runtime_break_glass_enabled", grant, principal); err == nil {
		t.Fatal("break-glass Audit persistence failure was swallowed")
	}
}
