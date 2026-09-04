package runtime

import (
	"context"
	"errors"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type integrationNotificationResourceReaderStub struct {
	secrets     []integrationsdk.Secret
	connections []integrationsdk.Connection
	err         error
}

func registerIntegrationNotificationActionAuthorizers(registry *notificationfacade.ActionAuthorizerRegistry, resources integrationNotificationResourceReaderStub) {
	if registry == nil {
		return
	}
	registry.Register("integration_secret", func(ctx context.Context, id string, principal principalmodel.Principal) error {
		if !principal.HasPermission("integration.secret.manage") {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		values, err := resources.ListSecrets(ctx, principal.WorkspaceID)
		if err != nil {
			return err
		}
		for _, value := range values {
			if value.Key == id {
				return nil
			}
		}
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
	})
	registry.Register("integration_connection", func(ctx context.Context, id string, principal principalmodel.Principal) error {
		if !principal.HasPermission("integration.connection.manage") {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		values, err := resources.ListConnections(ctx, principal.WorkspaceID)
		if err != nil {
			return err
		}
		for _, value := range values {
			if value.Key == id {
				return nil
			}
		}
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
	})
}

func (s integrationNotificationResourceReaderStub) ListSecrets(context.Context, string) ([]integrationsdk.Secret, error) {
	return s.secrets, s.err
}

func (s integrationNotificationResourceReaderStub) ListConnections(context.Context, string) ([]integrationsdk.Connection, error) {
	return s.connections, s.err
}

func TestIntegrationNotificationActionsReauthorizeManagementResourceAccess(t *testing.T) {
	failure := errors.New("resource lookup failed")
	for _, test := range []struct {
		name         string
		resourceType string
		resourceID   string
		permissions  []string
		reader       integrationNotificationResourceReaderStub
		wantCode     string
		wantErr      error
	}{
		{name: "secret allowed", resourceType: "integration_secret", resourceID: "secret", permissions: []string{"integration.secret.manage"}, reader: integrationNotificationResourceReaderStub{secrets: []integrationsdk.Secret{{Key: "secret"}}}},
		{name: "secret missing permission", resourceType: "integration_secret", resourceID: "secret", permissions: nil, reader: integrationNotificationResourceReaderStub{secrets: []integrationsdk.Secret{{Key: "secret"}}}, wantCode: "backend.notification.inbox_action_forbidden"},
		{name: "secret missing resource", resourceType: "integration_secret", resourceID: "missing", permissions: []string{"integration.secret.manage"}, reader: integrationNotificationResourceReaderStub{secrets: []integrationsdk.Secret{{Key: "secret"}}}, wantCode: "backend.notification.inbox_action_resource_not_found"},
		{name: "secret lookup failure", resourceType: "integration_secret", resourceID: "secret", permissions: []string{"integration.secret.manage"}, reader: integrationNotificationResourceReaderStub{err: failure}, wantErr: failure},
		{name: "connection allowed", resourceType: "integration_connection", resourceID: "erp", permissions: []string{"integration.connection.manage"}, reader: integrationNotificationResourceReaderStub{connections: []integrationsdk.Connection{{Key: "erp"}}}},
		{name: "connection missing manage", resourceType: "integration_connection", resourceID: "erp", permissions: nil, reader: integrationNotificationResourceReaderStub{connections: []integrationsdk.Connection{{Key: "erp"}}}, wantCode: "backend.notification.inbox_action_forbidden"},
		{name: "connection missing resource", resourceType: "integration_connection", resourceID: "missing", permissions: []string{"integration.connection.manage"}, reader: integrationNotificationResourceReaderStub{connections: []integrationsdk.Connection{{Key: "erp"}}}, wantCode: "backend.notification.inbox_action_resource_not_found"},
		{name: "connection lookup failure", resourceType: "integration_connection", resourceID: "erp", permissions: []string{"integration.connection.manage"}, reader: integrationNotificationResourceReaderStub{err: failure}, wantErr: failure},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := notificationfacade.NewActionAuthorizerRegistry()
			registerIntegrationNotificationActionAuthorizers(registry, test.reader)
			registry.Freeze()
			principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "actor"}}, accessfixture.Bundle{
				Permissions:  test.permissions,
				DataPolicies: accessfixture.DataPoliciesForPermissions(test.permissions, identitysdk.DataScopeAll),
			})
			err := registry.Authorize(t.Context(), notificationmodel.NotificationInboxResolvedAction{RouteParams: map[string]string{"resource_type": test.resourceType, "resource_id": test.resourceID}}, principal)
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v want=%v", err, test.wantErr)
			}
			if test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("err=%v code=%q want=%q", err, apperror.CodeOf(err), test.wantCode)
			}
			if test.wantErr == nil && test.wantCode == "" && err != nil {
				t.Fatal(err)
			}
		})
	}
	registerIntegrationNotificationActionAuthorizers(nil, integrationNotificationResourceReaderStub{})
}

func TestRuntimeNotificationActionAuthorizerBindingFailsClosedUntilWired(t *testing.T) {
	var binding *runtimeNotificationActionAuthorizerBinding
	if code := apperror.CodeOf(binding.Authorize(t.Context(), "report", principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
		t.Fatalf("nil binding code=%q", code)
	}
	binding = &runtimeNotificationActionAuthorizerBinding{}
	if code := apperror.CodeOf(binding.Authorize(t.Context(), "report", principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
		t.Fatalf("unwired binding code=%q", code)
	}
	want := errors.New("authorization failed")
	binding.authorize = func(context.Context, string, principalmodel.Principal) error { return want }
	if err := binding.Authorize(t.Context(), "report", principalmodel.Principal{}); !errors.Is(err, want) {
		t.Fatalf("wired binding error=%v", err)
	}
}

func TestRuntimeResolvedNotificationAndProjectRecordAuthorizersFailClosedAndDelegate(t *testing.T) {
	var binding *runtimeNotificationResolvedActionAuthorizerBinding
	action := notificationmodel.NotificationInboxResolvedAction{RouteParams: map[string]string{"object_key": "order", "resource_id": "order-1"}}
	if code := apperror.CodeOf(binding.Authorize(t.Context(), action, principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
		t.Fatalf("nil resolved binding code=%q", code)
	}
	binding = &runtimeNotificationResolvedActionAuthorizerBinding{}
	if code := apperror.CodeOf(binding.Authorize(t.Context(), action, principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
		t.Fatalf("unwired resolved binding code=%q", code)
	}
	wantErr := errors.New("resolved authorization failed")
	binding.authorize = func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error {
		return wantErr
	}
	if err := binding.Authorize(t.Context(), action, principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("resolved binding err=%v", err)
	}

	authorize := newProjectRecordNotificationActionAuthorizer(nil)
	if code := apperror.CodeOf(authorize(t.Context(), action, principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
		t.Fatalf("nil getter code=%q", code)
	}
	called := false
	authorize = newProjectRecordNotificationActionAuthorizer(func(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
		called = objectKey == "order" && recordID == "order-1"
		return recordmodel.Record{ID: recordID}, wantErr
	})
	for name, routeParams := range map[string]map[string]string{
		"missing object": {"resource_id": "order-1"},
		"missing record": {"object_key": "order"},
	} {
		t.Run(name, func(t *testing.T) {
			if code := apperror.CodeOf(authorize(t.Context(), notificationmodel.NotificationInboxResolvedAction{RouteParams: routeParams}, principalmodel.Principal{})); code != "backend.notification.inbox_action_unavailable" {
				t.Fatalf("code=%q", code)
			}
		})
	}
	if err := authorize(t.Context(), action, principalmodel.Principal{}); !errors.Is(err, wantErr) || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
