package integration

import (
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestRecordIntegrationEventWakesOnlyAfterSuccessfulAcceptance(t *testing.T) {
	repository := &integrationManagementEventRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: repository})
	principal := integrationManagementPrincipal(PermissionInvoke)
	request := integrationmodel.IntegrationEventRecordRequest{Provider: "provider", EventType: "created", ExternalID: "external", Status: "received"}
	event, _, err := service.RecordIntegrationEvent(t.Context(), request, principal)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case locator := <-IntegrationEventWakeups(service):
		if locator != (IntegrationEventLocator{WorkspaceID: principal.WorkspaceID, EventID: event.ID}) {
			t.Fatalf("locator=%#v", locator)
		}
	default:
		t.Fatal("accepted event did not wake the worker")
	}
	repository.err = errIntegrationManagementTest
	request.ExternalID = "failed"
	if _, _, err := service.RecordIntegrationEvent(t.Context(), request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("accept error=%v", err)
	}
	select {
	case locator := <-IntegrationEventWakeups(service):
		t.Fatalf("failed acceptance emitted wakeup=%#v", locator)
	default:
	}
}
