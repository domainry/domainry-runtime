package notification

import (
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestNotificationAdministrationRejectsMissingWorkspacePrincipalBeforeModuleAccess(t *testing.T) {
	service := NewNotificationApplicationService()
	principal := principalmodel.Principal{}
	checks := []func() error{
		func() error { _, err := service.List(t.Context(), principal); return err },
		func() error { _, _, err := service.Get(t.Context(), "template", principal); return err },
		func() error {
			_, err := service.SaveDraft(t.Context(), "template", notificationmodel.NotificationTemplate{}, "", principal)
			return err
		},
		func() error { _, err := service.GetDeliveryPolicy(t.Context(), principal); return err },
		func() error {
			_, err := service.SaveDeliveryPolicy(t.Context(), notificationmodel.NotificationDeliveryPolicy{}, principal)
			return err
		},
	}
	for _, check := range checks {
		if err := check(); apperror.CodeOf(err) != "backend.workspace_scope_required" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestNotificationWorkersRejectNonInstallationScopeBeforeModuleAccess(t *testing.T) {
	service := NewNotificationApplicationService()
	if err := service.RefreshPublished(t.Context(), principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("refresh error=%v", err)
	}
	if _, err := service.ProcessDuePublications(t.Context(), 10, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("publication error=%v", err)
	}
	if _, err := service.ProcessPublication(t.Context(), NotificationPublicationLocator{RequestID: "request"}, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("process error=%v", err)
	}
}
