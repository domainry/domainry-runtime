package transport

import (
	"context"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type breakGlassAuditRepository struct {
	workspaceID string
	event       auditmodel.AuditEvent
}

func (r *breakGlassAuditRepository) InsertAuditEvent(_ context.Context, workspaceID string, event auditmodel.AuditEvent) error {
	r.workspaceID, r.event = workspaceID, event
	return nil
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
		IncidentRef: "INC-1", AuditEventID: "audit-1", AlertTarget: "security", ApproverIDs: []string{"a", "b"}, Revision: 3,
	}
	if err := alert.BreakGlassAlert(t.Context(), "runtime_break_glass_enabled", grant, principal); err != nil {
		t.Fatal(err)
	}
	if repository.workspaceID != "workspace-a" || repository.event.Event != "runtime_break_glass_enabled" || repository.event.RecordID != grant.ID {
		t.Fatalf("workspace=%q event=%#v", repository.workspaceID, repository.event)
	}
	if repository.event.After["state"] != grant.State || repository.event.Metadata["audit_event_id"] != grant.AuditEventID || repository.event.Metadata["revision"] != grant.Revision {
		t.Fatalf("after=%#v metadata=%#v", repository.event.After, repository.event.Metadata)
	}
}
