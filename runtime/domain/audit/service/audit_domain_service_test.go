// Audit domain service tests.
package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"context"
	"errors"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
)

type repositoryStub struct {
	events        []auditmodel.AuditEvent
	options       []auditmodel.AuditOption
	insertErr     error
	listErr       error
	systemListErr error
	optionsErr    error
}

func TestAuditAppendModesPersistOnSuccess(t *testing.T) {
	repository := &repositoryStub{}
	service := NewAuditDomainService(repository)

	if err := service.AppendAudit(t.Context(), auditcontract.AuditAppendRequest{Event: "mandatory"}); err != nil {
		t.Fatalf("AppendAudit() error = %v", err)
	}
	service.AppendAuditTelemetry(t.Context(), auditcontract.AuditAppendRequest{Event: "telemetry"})
	if len(repository.events) != 2 || repository.events[0].Event != "mandatory" || repository.events[1].Event != "telemetry" {
		t.Fatalf("persisted events = %#v", repository.events)
	}
}

func (r *repositoryStub) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.events = append(r.events, event)
	return nil
}

func TestMandatoryAuditReturnsStorageFailureWhileTelemetryIsBestEffort(t *testing.T) {
	repository := &repositoryStub{insertErr: errors.New("storage unavailable")}
	service := NewAuditDomainService(repository)
	request := auditcontract.AuditAppendRequest{Event: "record_created", Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}}

	if err := service.AppendAudit(t.Context(), request); errorCode(err) != "backend.internal" {
		t.Fatalf("AppendAudit() error = %v", err)
	}
	service.AppendAuditTelemetry(t.Context(), request)
	if len(repository.events) != 0 {
		t.Fatalf("failed telemetry persisted events: %#v", repository.events)
	}
}

func TestAuditEventFactoryRedactsAndAddsPrincipalContext(t *testing.T) {
	service := NewAuditDomainService(&repositoryStub{})
	event := service.NewAuditEvent(t.Context(), auditcontract.AuditAppendRequest{
		Event: "integration_called", Principal: accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", WorkspaceID: "workspace-1"}, RequestID: "request-1"}, accessfixture.Bundle{Key: "operator"}),
		Metadata: map[string]any{"api_key": "secret", "safe": "visible"},
	})
	if event.Metadata["api_key"] != "[REDACTED]" || event.Metadata["safe"] != "visible" {
		t.Fatalf("metadata = %#v", event.Metadata)
	}
	if event.Metadata["workspace_id"] != "workspace-1" || event.Metadata["request_id"] != "request-1" || event.Metadata["role_key"] != "operator" {
		t.Fatalf("principal metadata = %#v", event.Metadata)
	}
	next := service.NewAuditEvent(t.Context(), auditcontract.AuditAppendRequest{Event: "integration_called", Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}})
	if next.ID == event.ID {
		t.Fatalf("audit IDs collided: %q", event.ID)
	}
}

func TestIdempotentAuditEventIgnoresPerAttemptRequestID(t *testing.T) {
	service := NewAuditDomainService(&repositoryStub{})
	request := auditcontract.AuditAppendRequest{
		IdempotencyKey: "report-export-download-prepared:artifact-1",
		Event:          "report_export_download_prepared",
		Principal:      accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", WorkspaceID: "workspace-1"}, RequestID: "attempt-1"}, accessfixture.Bundle{Key: "operator"}),
		Metadata:       map[string]any{"artifact_id": "artifact-1"},
	}
	first := service.NewAuditEvent(t.Context(), request)
	request.Principal.RequestID = "attempt-2"
	second := service.NewAuditEvent(t.Context(), request)
	if first.ID != second.ID {
		t.Fatalf("idempotent audit IDs differ: %q != %q", first.ID, second.ID)
	}
	if _, exists := first.Metadata["request_id"]; exists {
		t.Fatalf("idempotent audit captured per-attempt request ID: %#v", first.Metadata)
	}
	if _, exists := second.Metadata["request_id"]; exists {
		t.Fatalf("idempotent audit replay captured per-attempt request ID: %#v", second.Metadata)
	}
	if first.Metadata["artifact_id"] != "artifact-1" || second.Metadata["artifact_id"] != "artifact-1" {
		t.Fatalf("stable business metadata was lost: first=%#v second=%#v", first.Metadata, second.Metadata)
	}
}

func TestMandatoryAuditHonorsContextCancellation(t *testing.T) {
	repository := &repositoryStub{}
	service := NewAuditDomainService(repository)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := service.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendAudit() error = %v", err)
	}
	if len(repository.events) != 0 {
		t.Fatalf("cancelled append reached repository: %#v", repository.events)
	}
	service.AppendAuditTelemetry(ctx, auditcontract.AuditAppendRequest{Event: "cancelled_telemetry"})
	if len(repository.events) != 0 {
		t.Fatalf("cancelled telemetry reached repository: %#v", repository.events)
	}
}

func TestAuditAppendModesHandleUnavailableRepository(t *testing.T) {
	service := NewAuditDomainService(nil)
	request := auditcontract.AuditAppendRequest{Event: "repository_unavailable"}
	if err := service.AppendAudit(t.Context(), request); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("AppendAudit() error = %v", err)
	}
	service.AppendAuditTelemetry(t.Context(), request)
}
func (r *repositoryStub) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return append([]auditmodel.AuditEvent(nil), r.events...), r.listErr
}
func (r *repositoryStub) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return append([]auditmodel.AuditEvent(nil), r.events...), r.systemListErr
}
func (r *repositoryStub) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	if r.options != nil {
		return append([]auditmodel.AuditOption(nil), r.options...), r.optionsErr
	}
	return []auditmodel.AuditOption{{Value: "record-1", Label: "record-1"}}, r.optionsErr
}

func TestServiceUsesOnlyAuditRepository(t *testing.T) {
	repository := &repositoryStub{}
	application := NewAuditDomainService(repository)
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"identity.audit.view"}})
	application.AppendWithMetadata(t.Context(), "record_viewed", "customer", "record-1", admin, "viewed", nil, nil, map[string]any{"source": "test"})
	events, err := application.Events(t.Context(), auditmodel.AuditEventQuery{}, admin)
	if err != nil || len(events) != 1 || events[0].RecordID != "record-1" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	options, err := application.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "record_id"}, admin)
	if err != nil || len(options) != 1 {
		t.Fatalf("options=%#v err=%v", options, err)
	}
	if _, err := application.Events(t.Context(), auditmodel.AuditEventQuery{}, principalmodel.Principal{}); errorCode(err) != "backend.role.unknown" {
		t.Fatalf("unknown principal error=%v", err)
	}
	if _, err := application.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "password"}, admin); errorCode(err) != "backend.audit.option_field_invalid" {
		t.Fatalf("invalid option error=%v", err)
	}
}

func TestAuditQueriesEnforcePermissionsAndWrapRepositoryFailures(t *testing.T) {
	repository := &repositoryStub{events: []auditmodel.AuditEvent{{Event: "record_viewed"}}}
	service := NewAuditDomainService(repository)
	withoutPermission := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, withoutPermission); errorCode(err) != "backend.audit.view_permission_required" {
		t.Fatalf("events permission error=%v", err)
	}
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, principalmodel.Principal{}); errorCode(err) != "backend.role.unknown" {
		t.Fatalf("options unknown principal error=%v", err)
	}
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, withoutPermission); errorCode(err) != "backend.audit.view_permission_required" {
		t.Fatalf("options permission error=%v", err)
	}

	legacyReader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{"identity_permission.read"}})
	if events, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, legacyReader); err != nil || len(events) != 1 {
		t.Fatalf("legacy events=%#v err=%v", events, err)
	}
	for _, field := range []string{"record_id", "actor_id", "role_key", "event"} {
		if options, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: field}, legacyReader); err != nil || len(options) != 1 {
			t.Fatalf("field %q options=%#v err=%v", field, options, err)
		}
	}

	repository.listErr = errors.New("list failed")
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, legacyReader); errorCode(err) != "backend.internal" {
		t.Fatalf("events repository error=%v", err)
	}
	repository.optionsErr = errors.New("options failed")
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, legacyReader); errorCode(err) != "backend.internal" {
		t.Fatalf("options repository error=%v", err)
	}
}

func TestAuditReaderPortsHandleCancellationAvailabilityAndSystemScope(t *testing.T) {
	repository := &repositoryStub{events: []auditmodel.AuditEvent{{Event: "one"}}, options: []auditmodel.AuditOption{{Value: "one"}}}
	service := NewAuditDomainService(repository)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ListAuditEvents(cancelled, "workspace", auditmodel.AuditEventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("event cancellation=%v", err)
	}
	if _, err := service.ListAuditEventsForSystem(cancelled, principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("system event cancellation=%v", err)
	}
	if _, err := service.ListAuditOptions(cancelled, "workspace", auditmodel.AuditOptionQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("option cancellation=%v", err)
	}

	var nilService *AuditDomainService
	if err := nilService.AppendAudit(t.Context(), auditcontract.AuditAppendRequest{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil append service error=%v", err)
	}
	if _, err := nilService.ListAuditEvents(t.Context(), "workspace", auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil event service error=%v", err)
	}
	if _, err := nilService.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil system service error=%v", err)
	}
	if _, err := nilService.ListAuditOptions(t.Context(), "workspace", auditmodel.AuditOptionQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil option service error=%v", err)
	}

	unavailable := NewAuditDomainService(nil)
	if _, err := unavailable.ListAuditEvents(t.Context(), "workspace", auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil event repository error=%v", err)
	}
	if _, err := unavailable.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil system repository error=%v", err)
	}
	if _, err := unavailable.ListAuditOptions(t.Context(), "workspace", auditmodel.AuditOptionQuery{}); errorCode(err) != "backend.audit.repository_unavailable" {
		t.Fatalf("nil option repository error=%v", err)
	}

	if events, err := service.ListAuditEvents(t.Context(), "workspace", auditmodel.AuditEventQuery{}); err != nil || len(events) != 1 {
		t.Fatalf("direct events=%#v err=%v", events, err)
	}
	if events, err := service.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); err != nil || len(events) != 1 {
		t.Fatalf("system events=%#v err=%v", events, err)
	}
	if options, err := service.ListAuditOptions(t.Context(), "workspace", auditmodel.AuditOptionQuery{}); err != nil || len(options) != 1 {
		t.Fatalf("direct options=%#v err=%v", options, err)
	}
}

func errorCode(err error) string {
	if coded, ok := err.(interface{ ErrorCode() string }); ok {
		return coded.ErrorCode()
	}
	return ""
}
