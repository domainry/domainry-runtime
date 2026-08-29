package service

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type auditEdgeRepository struct {
	events     []auditmodel.AuditEvent
	options    []auditmodel.AuditOption
	eventsErr  error
	systemErr  error
	optionsErr error
}

func (r *auditEdgeRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	return nil
}

func (r *auditEdgeRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return r.events, r.eventsErr
}

func (r *auditEdgeRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return r.events, r.systemErr
}

func (r *auditEdgeRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return r.options, r.optionsErr
}

func TestAuditQueriesCoverAuthorizationFieldsAndRepositoryFailures(t *testing.T) {
	want := errors.New("audit store unavailable")
	repository := &auditEdgeRepository{
		events:  []auditmodel.AuditEvent{{ID: "audit-1"}},
		options: []auditmodel.AuditOption{{Value: "record-1"}},
	}
	service := NewAuditDomainService(repository)
	unknown := principalmodel.Principal{}
	denied := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	legacyReader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: " workspace-a "}}, accessfixture.Bundle{Permissions: []string{"identity_permission.read"}})

	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, unknown); errorCode(err) != "backend.role.unknown" {
		t.Fatalf("unknown events error=%v", err)
	}
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, denied); errorCode(err) != "backend.audit.view_permission_required" {
		t.Fatalf("denied events error=%v", err)
	}
	if events, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, legacyReader); err != nil || len(events) != 1 {
		t.Fatalf("legacy reader events=%#v err=%v", events, err)
	}
	repository.eventsErr = want
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, legacyReader); errorCode(err) != "backend.internal" || !errors.Is(err, want) {
		t.Fatalf("events repository error=%v", err)
	}
	repository.eventsErr = nil

	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "record_id"}, unknown); errorCode(err) != "backend.role.unknown" {
		t.Fatalf("unknown options error=%v", err)
	}
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "record_id"}, denied); errorCode(err) != "backend.audit.view_permission_required" {
		t.Fatalf("denied options error=%v", err)
	}
	for _, field := range []string{"record_id", "actor_id", "role_key", "event"} {
		options, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: field}, legacyReader)
		if err != nil || len(options) != 1 {
			t.Fatalf("field %q options=%#v err=%v", field, options, err)
		}
	}
	repository.optionsErr = want
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, legacyReader); errorCode(err) != "backend.internal" || !errors.Is(err, want) {
		t.Fatalf("options repository error=%v", err)
	}
}

func TestAuditReadersCoverCancellationUnavailableAndRepositoryResults(t *testing.T) {
	want := errors.New("audit read failed")
	repository := &auditEdgeRepository{
		events:  []auditmodel.AuditEvent{{ID: "audit-1"}},
		options: []auditmodel.AuditOption{{Value: "record-1"}},
	}
	service := NewAuditDomainService(repository)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := service.ListAuditEvents(cancelled, "workspace-a", auditmodel.AuditEventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("events cancellation=%v", err)
	}
	if _, err := service.ListAuditEventsForSystem(cancelled, principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("system events cancellation=%v", err)
	}
	if _, err := service.ListAuditOptions(cancelled, "workspace-a", auditmodel.AuditOptionQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("options cancellation=%v", err)
	}

	var nilService *AuditDomainService
	if _, err := nilService.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil events service error=%v", err)
	}
	if _, err := nilService.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil system service error=%v", err)
	}
	if _, err := nilService.ListAuditOptions(t.Context(), "workspace-a", auditmodel.AuditOptionQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil options service error=%v", err)
	}
	if err := nilService.AppendAudit(t.Context(), auditAppendRequest("nil-service")); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil append service error=%v", err)
	}

	if events, err := service.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{}); err != nil || len(events) != 1 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if events, err := service.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); err != nil || len(events) != 1 {
		t.Fatalf("system events=%#v err=%v", events, err)
	}
	if options, err := service.ListAuditOptions(t.Context(), "workspace-a", auditmodel.AuditOptionQuery{}); err != nil || len(options) != 1 {
		t.Fatalf("options=%#v err=%v", options, err)
	}

	repository.eventsErr, repository.systemErr, repository.optionsErr = want, want, want
	if _, err := service.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{}); !errors.Is(err, want) {
		t.Fatalf("events error=%v", err)
	}
	if _, err := service.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); !errors.Is(err, want) {
		t.Fatalf("system events error=%v", err)
	}
	if _, err := service.ListAuditOptions(t.Context(), "workspace-a", auditmodel.AuditOptionQuery{}); !errors.Is(err, want) {
		t.Fatalf("options error=%v", err)
	}
}

func auditAppendRequest(event string) auditcontract.AuditAppendRequest {
	return auditcontract.AuditAppendRequest{Event: event}
}
