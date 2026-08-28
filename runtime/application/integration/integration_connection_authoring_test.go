package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationConnectionHistory struct {
	events []auditmodel.AuditEvent
	err    error
}

func (h integrationConnectionHistory) Events(context.Context, auditmodel.AuditEventQuery, principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	return append([]auditmodel.AuditEvent(nil), h.events...), h.err
}

func TestIntegrationConnectionAuthoringReadsResourceAndAuditRevisions(t *testing.T) {
	repository := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{Key: "primary", ConnectorKey: "crm", ProviderKey: "default", Status: "verified"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		ConnectionHistory: integrationConnectionHistory{events: []auditmodel.AuditEvent{
			{ID: "revision-1", Event: "integration_connection_upserted", After: map[string]any{"key": "primary"}},
			{ID: "ignored", Event: "integration_invocation_created"},
		}},
	})
	principal := integrationManagementPrincipal(PermissionCatalogView)

	connection, err := service.GetIntegrationConnection(t.Context(), "primary", principal)
	if err != nil || connection.Key != "primary" {
		t.Fatalf("connection=%#v err=%v", connection, err)
	}
	versions, err := service.IntegrationConnectionVersions(t.Context(), "primary", principal)
	if err != nil || len(versions) != 1 || versions[0].RevisionID != "revision-1" {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}
	if _, err := service.GetIntegrationConnection(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("missing error=%v", err)
	}
	withoutHistory := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if _, err := withoutHistory.IntegrationConnectionVersions(t.Context(), "primary", principal); apperror.CodeOf(err) != "backend.integration.connection.history_unavailable" {
		t.Fatalf("history error=%v", err)
	}
}

func TestIntegrationConnectionAuthoringAuthorizationAndFailureEdges(t *testing.T) {
	repository := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{Key: "primary", Status: "verified"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	allowed := integrationManagementPrincipal(PermissionConnectionManage, PermissionCatalogView)
	denied := integrationManagementPrincipal(PermissionCatalogView)

	if err := service.AuthorizeIntegrationConnectionUpsert(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal authorization error=%v", err)
	}
	if err := service.AuthorizeIntegrationConnectionUpsert(denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission authorization error=%v", err)
	}
	if err := service.AuthorizeIntegrationConnectionUpsert(allowed); err != nil {
		t.Fatalf("authorized connection upsert=%v", err)
	}

	if hash, found, err := service.IntegrationConnectionAuthoringHash(t.Context(), " primary ", allowed); err != nil || !found || hash == "" {
		t.Fatalf("existing hash=%q found=%v err=%v", hash, found, err)
	}
	if hash, found, err := service.IntegrationConnectionAuthoringHash(t.Context(), "missing", allowed); err != nil || found || hash != "" {
		t.Fatalf("missing hash=%q found=%v err=%v", hash, found, err)
	}
	repository.err = errors.New("connection repository unavailable")
	if _, _, err := service.IntegrationConnectionAuthoringHash(t.Context(), "primary", allowed); !errors.Is(err, repository.err) {
		t.Fatalf("hash repository error=%v", err)
	}
	if _, err := service.GetIntegrationConnection(t.Context(), "primary", allowed); !errors.Is(err, repository.err) {
		t.Fatalf("get repository error=%v", err)
	}
	if _, err := service.IntegrationConnectionVersions(t.Context(), "primary", allowed); !errors.Is(err, repository.err) {
		t.Fatalf("versions connection error=%v", err)
	}

	repository.err = nil
	historyErr := errors.New("connection history unavailable")
	withFailingHistory := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository:  repository,
		ConnectionHistory: integrationConnectionHistory{err: historyErr},
	})
	if _, err := withFailingHistory.IntegrationConnectionVersions(t.Context(), "primary", allowed); !errors.Is(err, historyErr) {
		t.Fatalf("versions history error=%v", err)
	}
}
