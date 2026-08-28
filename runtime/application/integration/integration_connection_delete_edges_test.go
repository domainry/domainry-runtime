package integration

import (
	"context"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type connectionDeleteEdgeRepository struct {
	integrationrepository.IntegrationConfigRepository
	connections     []integrationmodel.IntegrationConnection
	subscriptions   []integrationmodel.IntegrationWebhookSubscription
	listErr         error
	subscriptionErr error
	deleteErr       error
	deleted         bool
	removeOnDelete  bool
	cancel          context.CancelFunc
	listCalls       int
	listErrAfter    int
}

func (r *connectionDeleteEdgeRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	r.listCalls++
	if r.listErr != nil && (r.listErrAfter == 0 || r.listCalls >= r.listErrAfter) {
		return nil, r.listErr
	}
	return append([]integrationmodel.IntegrationConnection(nil), r.connections...), nil
}
func (r *connectionDeleteEdgeRepository) ListWebhookSubscriptions(context.Context, string, string, string, string, int) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	return append([]integrationmodel.IntegrationWebhookSubscription(nil), r.subscriptions...), r.subscriptionErr
}
func (r *connectionDeleteEdgeRepository) DeleteConnection(context.Context, string, string) (bool, error) {
	if r.deleteErr != nil {
		return false, r.deleteErr
	}
	if r.removeOnDelete {
		r.connections = nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.deleted, nil
}

func TestDeleteIntegrationConnectionEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionConnectionManage)
	existing := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", Status: "active"}
	repository := &connectionDeleteEdgeRepository{connections: []integrationmodel.IntegrationConnection{existing}, deleted: true}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.DeleteIntegrationConnection(cancelled, "connection", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection list error=%v", err)
	}
	repository.listErr, repository.connections = nil, nil
	if err := service.DeleteIntegrationConnection(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("missing connection error=%v", err)
	}
	repository.connections = []integrationmodel.IntegrationConnection{existing}
	service.connectionReferences = func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
		return nil, errIntegrationManagementTest
	}
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("reference error=%v", err)
	}
	service.connectionReferences = func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
		return []ConnectionReference{{Kind: "action", Key: "action", Path: "config.connection_key"}}, nil
	}
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); apperror.CodeOf(err) != "backend.integration.connection.referenced" {
		t.Fatalf("reference conflict=%v", err)
	}
	service.connectionReferences = func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
		return nil, nil
	}
	repository.subscriptionErr = errIntegrationManagementTest
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("subscription error=%v", err)
	}
	repository.subscriptionErr = nil
	repository.subscriptions = []integrationmodel.IntegrationWebhookSubscription{{Key: "subscription", ConnectionKey: "connection"}}
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); apperror.CodeOf(err) != "backend.integration.connection.referenced" {
		t.Fatalf("subscription conflict=%v", err)
	}
	repository.subscriptions = nil
	repository.deleteErr = errIntegrationManagementTest
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("delete error=%v", err)
	}
	repository.deleteErr, repository.deleted = nil, true
	if err := service.DeleteIntegrationConnection(t.Context(), " connection ", principal); err != nil {
		t.Fatalf("delete success=%v", err)
	}
}

func TestDeleteIntegrationConnectionFalseDeleteEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionConnectionManage)
	existing := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector"}
	repository := &connectionDeleteEdgeRepository{connections: []integrationmodel.IntegrationConnection{existing}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); apperror.CodeOf(err) != "backend.integration.connection.referenced" {
		t.Fatalf("still-existing conflict=%v", err)
	}
	repository.listCalls, repository.removeOnDelete = 0, true
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("vanished connection error=%v", err)
	}
	repository.connections, repository.removeOnDelete, repository.listCalls = []integrationmodel.IntegrationConnection{existing}, false, 0
	repository.listErr, repository.listErrAfter = errIntegrationManagementTest, 2
	if err := service.DeleteIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("post-delete lookup error=%v", err)
	}
	repository.listErr, repository.listErrAfter, repository.listCalls = nil, 0, 0
	ctx, cancel := context.WithCancel(t.Context())
	repository.removeOnDelete, repository.cancel = true, cancel
	if err := service.DeleteIntegrationConnection(ctx, "connection", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-delete cancellation error=%v", err)
	}

	unsupported := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{existing}}
	if err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: unsupported}).DeleteIntegrationConnection(t.Context(), "connection", principal); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("unsupported repository error=%v", err)
	}
}
