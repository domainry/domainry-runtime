package integration

import (
	"context"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationOfflineConflictRepository struct{ integrationManagementEventRepo }

func (*integrationOfflineConflictRepository) AcceptEvent(_ context.Context, _ string, event integrationmodel.IntegrationEvent, _ integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error) {
	event.ID = "conflict"
	event.Error = "backend.integration.event.external_id_conflict"
	return event, true, nil
}

func TestRecoverOfflineIntegrationEventsRemainingBoundaries(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: &integrationManagementEventRepo{}})
	request := integrationmodel.IntegrationOfflineEventRecoveryRequest{Events: []integrationmodel.IntegrationEventRecordRequest{{Provider: "crm", EventType: "created", ExternalID: "external"}}}
	if _, err := service.RecoverOfflineIntegrationEvents(t.Context(), request, principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal accepted")
	}
	if _, err := service.RecoverOfflineIntegrationEvents(t.Context(), request, integrationManagementPrincipal()); err == nil {
		t.Fatal("missing invoke permission accepted")
	}
	principal := integrationManagementPrincipal(PermissionInvoke)
	if _, err := service.RecoverOfflineIntegrationEvents(t.Context(), integrationmodel.IntegrationOfflineEventRecoveryRequest{}, principal); err == nil {
		t.Fatal("empty batch accepted")
	}
	tooMany := make([]integrationmodel.IntegrationEventRecordRequest, 501)
	if _, err := service.RecoverOfflineIntegrationEvents(t.Context(), integrationmodel.IntegrationOfflineEventRecoveryRequest{Events: tooMany}, principal); err == nil {
		t.Fatal("oversized batch accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.RecoverOfflineIntegrationEvents(ctx, request, principal); err == nil {
		t.Fatal("cancelled recovery continued")
	}
	service = NewIntegrationApplicationService(ApplicationDependencies{EventRepository: &integrationManagementEventRepo{err: errIntegrationManagementTest}})
	if _, err := service.RecoverOfflineIntegrationEvents(t.Context(), request, principal); err == nil {
		t.Fatal("repository failure swallowed")
	}
	service = NewIntegrationApplicationService(ApplicationDependencies{EventRepository: &integrationOfflineConflictRepository{}})
	result, err := service.RecoverOfflineIntegrationEvents(t.Context(), request, principal)
	if err != nil || result.ReconciliationRequired != 1 || len(result.ConflictExternalIDs) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	duplicateRepository := &integrationManagementEventRepo{duplicate: true}
	service = NewIntegrationApplicationService(ApplicationDependencies{EventRepository: duplicateRepository})
	result, err = service.RecoverOfflineIntegrationEvents(t.Context(), request, principal)
	if err != nil || result.Duplicates != 1 {
		t.Fatalf("duplicate result=%+v err=%v", result, err)
	}
	service = NewIntegrationApplicationService(ApplicationDependencies{EventRepository: &integrationManagementEventRepo{}})
	result, err = service.RecoverOfflineIntegrationEvents(t.Context(), request, principal)
	if err != nil || result.Accepted != 1 {
		t.Fatalf("accepted result=%+v err=%v", result, err)
	}
	if _, found, err := service.findExternalIdentityBySubject(t.Context(), "provider", "user", "subject", "workspace"); err != nil || found {
		t.Fatalf("nil config identity found=%v err=%v", found, err)
	}
}
