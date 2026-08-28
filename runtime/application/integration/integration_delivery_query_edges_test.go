package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationDeliveryQueryAndErrorHelperEdges(t *testing.T) {
	repository := &integrationManagementDeliveryRepo{invocations: []integrationmodel.IntegrationInvocation{{ID: "invocation"}, {ID: "second"}}, outboxes: []integrationmodel.IntegrationOutboxMessage{{ID: "message"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	principal := integrationManagementPrincipal(PermissionAuditView)
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 0, principal); err != nil || len(values) != 2 {
		t.Fatalf("default invocation query=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "external", 10, principal); err != nil || len(values) != 0 {
		t.Fatalf("external-only invocation query=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 1, principal); err != nil || len(values) != 1 {
		t.Fatalf("bounded invocation query=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "provider", "external", 201, principal); err != nil || len(values) != 0 {
		t.Fatalf("filtered invocation query=%#v err=%v", values, err)
	}
	if _, err := service.ListIntegrationOutboxMessages(t.Context(), "", "", 1, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("outbox query authorization error=%v", err)
	}
	if _, err := service.ListIntegrationOutboxMessages(t.Context(), "", "", 1, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("outbox query permission error=%v", err)
	}
	if values, err := service.ListIntegrationOutboxMessages(t.Context(), " connector ", " failed ", 0, principal); err != nil || len(values) != 1 || repository.lastLimit != 100 {
		t.Fatalf("default-limit values=%#v limit=%d err=%v", values, repository.lastLimit, err)
	}
	if values, err := service.ListIntegrationOutboxMessages(t.Context(), "", "", 201, principal); err != nil || len(values) != 1 || repository.lastLimit != 100 {
		t.Fatalf("capped values=%#v limit=%d err=%v", values, repository.lastLimit, err)
	}
	repository.err = errIntegrationManagementTest
	if _, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 1, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation query repository error=%v", err)
	}
	if _, err := service.ListIntegrationOutboxMessages(t.Context(), "", "", 1, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox query repository error=%v", err)
	}
	if _, err := service.InspectIntegrationOutboxMessage(t.Context(), "message", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("inspect authorization error=%v", err)
	}
	if principalWorkspaceID(principalmodel.Principal{}) != "default" || principalWorkspaceID(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: " workspace "}}) != "workspace" {
		t.Fatal("principal workspace normalization mismatch")
	}
	for _, err := range []error{
		badRequest("bad", "", "ignored", "key", "value", "odd"),
		conflict("conflict", "", "ignored", "key", "value", "odd"),
		notFound("missing", "", "ignored", "key", "value", "odd"),
	} {
		var appErr *apperror.AppError
		if !errors.As(err, &appErr) || appErr.Params["key"] != "value" {
			t.Fatalf("parameterized error=%#v", err)
		}
	}
}

func TestIntegrationDeliveryMetadataConditionEdges(t *testing.T) {
	repository := &deliveryCommandRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository, ConnectorExists: func(string) bool { return true }, InvocationProviderResolver: func(context.Context, string, string, string, string) (string, error) { return "provider", nil }})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{PermissionInvoke}})
	saved, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "call", Metadata: map[string]any{"value": true}}, principal)
	if err != nil || saved.Metadata["value"] != true || saved.Metadata["request_id"] != nil || saved.Metadata["actor_id"] != nil || saved.Metadata["role_key"] != nil {
		t.Fatalf("metadata invocation=%#v err=%v", saved, err)
	}
	principal.RequestID = "request"
	ctx := requestcontext.WithCorrelationID(t.Context(), "correlation")
	message, err := service.EnqueueIntegrationOutboxMessage(ctx, integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "call"}, principal)
	if err != nil || message.RequestRef != "request" {
		t.Fatalf("correlated message=%#v err=%v", message, err)
	}
}

func TestIntegrationStatusMutationEdges(t *testing.T) {
	events := &integrationManagementEventRepo{event: integrationmodel.IntegrationEvent{ID: "event"}, found: true}
	delivery := &integrationManagementDeliveryRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events, DeliveryRepository: delivery})
	unknown := principalmodel.Principal{}
	if _, err := service.UpdateIntegrationEventStatus(t.Context(), "event", integrationmodel.IntegrationEventStatusRequest{}, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("event authorization error=%v", err)
	}
	if _, err := service.UpdateIntegrationInvocationStatus(t.Context(), "invocation", integrationmodel.IntegrationInvocationStatusRequest{}, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("invocation authorization error=%v", err)
	}
	if _, err := service.UpdateIntegrationOutboxStatus(t.Context(), "message", integrationmodel.IntegrationOutboxStatusRequest{}, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("outbox authorization error=%v", err)
	}
	principal := integrationManagementPrincipal(PermissionInvoke)
	if _, err := service.UpdateIntegrationInvocationStatus(t.Context(), "invocation", integrationmodel.IntegrationInvocationStatusRequest{Status: "invalid"}, principal); apperror.CodeOf(err) != "backend.integration.invocation.invalid_status" {
		t.Fatalf("invocation status error=%v", err)
	}
	if _, err := service.UpdateIntegrationOutboxStatus(t.Context(), "message", integrationmodel.IntegrationOutboxStatusRequest{Status: "invalid"}, principal); apperror.CodeOf(err) != "backend.integration.outbox.invalid_status" {
		t.Fatalf("outbox status error=%v", err)
	}
	events.updateErr = errIntegrationManagementTest
	if _, err := service.UpdateIntegrationEventStatus(t.Context(), "event", integrationmodel.IntegrationEventStatusRequest{Status: "failed"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("event update error=%v", err)
	}
	delivery.err = errIntegrationManagementTest
	if _, err := service.UpdateIntegrationInvocationStatus(t.Context(), "invocation", integrationmodel.IntegrationInvocationStatusRequest{Status: "failed"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation update error=%v", err)
	}
	if _, err := service.UpdateIntegrationOutboxStatus(t.Context(), "message", integrationmodel.IntegrationOutboxStatusRequest{Status: "failed"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox update error=%v", err)
	}
}
