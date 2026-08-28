package integration

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type workspaceAuthorizationConfigProbe struct {
	integrationrepository.IntegrationConfigRepository
	calls *int
}

func (p workspaceAuthorizationConfigProbe) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	*p.calls++
	return nil, nil
}

type workspaceAuthorizationEventProbe struct {
	integrationrepository.IntegrationEventRepository
	calls *int
}

func (p workspaceAuthorizationEventProbe) ListEvents(context.Context, string, string, string, int) ([]integrationmodel.IntegrationEvent, error) {
	*p.calls++
	return nil, nil
}

type workspaceAuthorizationDeliveryProbe struct {
	integrationrepository.IntegrationDeliveryRepository
	calls *int
}

func (p workspaceAuthorizationDeliveryProbe) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	*p.calls++
	return nil, nil
}

type workspaceAuthorizationWorkerProbe struct {
	integrationrepository.IntegrationWorkerRepository
	calls *int
}

func (p workspaceAuthorizationWorkerProbe) ListDueEvents(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationEvent, error) {
	*p.calls++
	return nil, nil
}

func TestIntegrationApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	calls := 0
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository:   workspaceAuthorizationConfigProbe{calls: &calls},
		EventRepository:    workspaceAuthorizationEventProbe{calls: &calls},
		DeliveryRepository: workspaceAuthorizationDeliveryProbe{calls: &calls},
		WorkerRepository:   workspaceAuthorizationWorkerProbe{calls: &calls},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{
		PermissionCatalogView, PermissionConnectionManage, PermissionSecretManage,
		PermissionInvoke, PermissionRetry, PermissionAuditView, "workspace.admin",
	}})
	checks := []func() error{
		func() error { _, err := service.ListIntegrationConnections(t.Context(), principal); return err },
		func() error { _, err := service.ListIntegrationEvents(t.Context(), "", "", 1, principal); return err },
		func() error {
			_, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 1, principal)
			return err
		},
		func() error { _, err := service.ProcessDueIntegrationEvents(t.Context(), 1, principal); return err },
		func() error {
			_, _, err := service.RecordIntegrationEvent(t.Context(), integrationmodel.IntegrationEventRecordRequest{}, principal)
			return err
		},
		func() error {
			_, err := service.UpsertIntegrationConnection(t.Context(), "connection", integrationmodel.IntegrationConnectionUpsertRequest{}, principal)
			return err
		},
	}
	for index, check := range checks {
		if code := apperror.CodeOf(check()); code != "backend.workspace_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	if calls != 0 {
		t.Fatalf("repositories called before workspace authorization: %d", calls)
	}
}
