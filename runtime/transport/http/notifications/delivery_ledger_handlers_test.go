package notifications

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type notificationHTTPRepository struct{}
type notificationHTTPResponse struct {
	status int
	code   string
	value  any
	err    error
}

func newNotificationHTTPHandler(_ *notificationHTTPRepository) (*NotificationsHandler, *notificationHTTPResponse, *principalmodel.Principal) {
	response := &notificationHTTPResponse{}
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reviewer", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{})
	return NewNotificationsHandler(NotificationsDependencies{
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { response.status, response.value = status, value },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			response.status, response.code = status, code
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			response.status, response.err = http.StatusInternalServerError, err
		},
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
	}), response, principal
}

type notificationDeliveryLedgerStub struct {
	values []integrationmodel.IntegrationOutboxMessage
	status string
	limit  int
	err    error
}

func (s *notificationDeliveryLedgerStub) ListIntegrationOutboxMessages(_ context.Context, _ string, status string, limit int, _ principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	s.status, s.limit = status, limit
	return append([]integrationmodel.IntegrationOutboxMessage(nil), s.values...), s.err
}

func TestNotificationDeliveryLedgerProjectsOnlyNotificationOutbox(t *testing.T) {
	handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
	accessfixture.Mutate(principal, func(role *accessfixture.Bundle) {
		role.Permissions = append(role.Permissions, "integration.audit.view")
	})
	ledger := &notificationDeliveryLedgerStub{values: []integrationmodel.IntegrationOutboxMessage{
		{ID: "notification-1", WorkspaceID: "workspace-1", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send_email", Status: "delivered", Payload: map[string]any{"template_key": "account.welcome", "recipient": "person@example.com"}, ResponseRef: "provider-1", AttemptCount: 1},
		{ID: "automation-1", ConnectorKey: "__automation__", Operation: "record.after_create", Status: "queued", Payload: map[string]any{"rule_key": "rule-1"}},
	}}
	handler.deliveryLedger = ledger

	handler.listDeliveries(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/deliveries?status=delivered&limit=50", nil))

	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("response = status %d code %q err %v", response.status, response.code, response.err)
	}
	payload, ok := response.value.(map[string]any)
	if !ok || payload["count"] != 1 {
		t.Fatalf("payload = %#v", response.value)
	}
	deliveries, ok := payload["deliveries"].([]notificationDeliveryProjection)
	if !ok || len(deliveries) != 1 || deliveries[0].ID != "notification-1" || deliveries[0].ResponseRef != "provider-1" {
		t.Fatalf("deliveries = %#v", payload["deliveries"])
	}
	if ledger.status != "delivered" || ledger.limit != 50 {
		t.Fatalf("query = status %q limit %d", ledger.status, ledger.limit)
	}
}

func TestNotificationDeliveryLedgerDefaultsMissingLimit(t *testing.T) {
	handler, _, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
	accessfixture.Mutate(principal, func(role *accessfixture.Bundle) {
		role.Permissions = append(role.Permissions, "integration.audit.view")
	})
	ledger := &notificationDeliveryLedgerStub{}
	handler.deliveryLedger = ledger
	handler.listDeliveries(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/deliveries", nil))
	if ledger.limit != 100 {
		t.Fatalf("default limit=%d", ledger.limit)
	}
}

func TestNotificationDeliveryLedgerPermissionAndFailureBoundaries(t *testing.T) {
	t.Run("permission required", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Set(principal, accessfixture.Bundle{})
		handler.deliveryLedger = &notificationDeliveryLedgerStub{}
		handler.listDeliveries(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/deliveries", nil))
		if response.status != http.StatusForbidden || response.code != "auth.permission_denied" {
			t.Fatalf("response = status %d code %q", response.status, response.code)
		}
	})
	t.Run("ledger unavailable", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Mutate(principal, func(role *accessfixture.Bundle) {
			role.Permissions = append(role.Permissions, "integration.audit.view")
		})
		handler.listDeliveries(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/deliveries", nil))
		if response.status != http.StatusServiceUnavailable || response.code != "backend.notification.delivery_ledger_unavailable" {
			t.Fatalf("response = status %d code %q", response.status, response.code)
		}
	})
	t.Run("ledger failure", func(t *testing.T) {
		handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
		accessfixture.Mutate(principal, func(role *accessfixture.Bundle) {
			role.Permissions = append(role.Permissions, "integration.audit.view")
		})
		failure := errors.New("ledger failed")
		handler.deliveryLedger = &notificationDeliveryLedgerStub{err: failure}
		handler.listDeliveries(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/notifications/deliveries?limit=999", nil))
		if response.status != http.StatusInternalServerError || !errors.Is(response.err, failure) {
			t.Fatalf("response = status %d err %v", response.status, response.err)
		}
	})
}

func TestNotificationDeliveryProjectionRejectsBlankTemplateKeys(t *testing.T) {
	if notificationOutboxMessage(integrationmodel.IntegrationOutboxMessage{Payload: map[string]any{"template_key": " "}}) {
		t.Fatal("blank template key must not enter the notification ledger")
	}
	projected := projectNotificationDelivery(integrationmodel.IntegrationOutboxMessage{ID: "delivery", ConnectorKey: "email", Status: "sent", Payload: map[string]any{"template_key": "notice"}})
	if projected.ID != "delivery" || projected.ConnectorKey != "email" || projected.Status != "sent" {
		t.Fatalf("projection = %#v", projected)
	}
}
