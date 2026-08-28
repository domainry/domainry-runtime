package audit

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type auditApplicationEdgeRepository struct {
	events  []auditmodel.AuditEvent
	options []auditmodel.AuditOption
	err     error
	inserts int
	queries int
}

func (r *auditApplicationEdgeRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	r.inserts++
	return r.err
}
func (r *auditApplicationEdgeRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	r.queries++
	return r.events, r.err
}
func (r *auditApplicationEdgeRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return r.events, r.err
}
func (r *auditApplicationEdgeRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	r.queries++
	return r.options, r.err
}

func TestAuditApplicationHappyErrorAndHelpers(t *testing.T) {
	repository := &auditApplicationEdgeRepository{events: []auditmodel.AuditEvent{{Event: "created"}}, options: []auditmodel.AuditOption{{Value: "created"}}}
	service := NewAuditApplicationService(repository)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: " workspace-1 ", UserID: "user-1"}}, accessfixture.Bundle{Permissions: []string{"identity.audit.view"}})
	service.Append(t.Context(), "created", "order", "record-1", principal, "created", nil, map[string]any{"password": "secret"})
	service.AppendWithMetadata(t.Context(), "updated", "order", "record-1", principal, "updated", nil, nil, map[string]any{"source": "test"})
	if repository.inserts != 2 {
		t.Fatalf("inserts=%d", repository.inserts)
	}
	events, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, principal)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	service.SetEventProjector(func(_ context.Context, events []auditmodel.AuditEvent, _ principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
		projected := append([]auditmodel.AuditEvent(nil), events...)
		projected[0].Before = map[string]any{"phone": "********5678"}
		return projected, nil
	})
	events, err = service.Events(t.Context(), auditmodel.AuditEventQuery{}, principal)
	if err != nil || events[0].Before["phone"] != "********5678" {
		t.Fatalf("audit presentation projector events=%v err=%v", events, err)
	}
	options, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, principal)
	if err != nil || len(options) != 1 {
		t.Fatalf("options=%v err=%v", options, err)
	}

	repository.err = errors.New("store failed")
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, principal); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("events error=%v", err)
	}
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, principal); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("options error=%v", err)
	}

	var nilService *AuditApplicationService
	nilService.SetEventProjector(nil)
	nilService.AppendWithMetadata(t.Context(), "event", "object", "record", principal, "summary", nil, nil, nil)
	(&AuditApplicationService{}).AppendWithMetadata(t.Context(), "event", "object", "record", principal, "summary", nil, nil, nil)
	unknown := principal
	unknown.Known = false
	service.AppendWithMetadata(t.Context(), "event", "object", "record", unknown, "summary", nil, nil, nil)

	event := AuditBuildEvent(t.Context(), "created", "order", "record-1", principal, "summary", nil, map[string]any{"value": 1}, nil)
	if event.Event != "created" || event.WorkspaceID != "workspace-1" {
		t.Fatalf("event=%+v", event)
	}
	redacted := AuditRedactSensitiveMap(map[string]any{"password": "secret", "name": "visible"})
	if redacted["password"] == "secret" || redacted["name"] != "visible" {
		t.Fatalf("redacted=%v", redacted)
	}
	if AuditPrincipalWorkspaceID(principal) != "workspace-1" || AuditPrincipalWorkspaceID(principalmodel.Principal{}) != "default" {
		t.Fatal("workspace fallback mismatch")
	}
}
