package notifications

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestLegacyPublishEndpointCannotBypassPublicationApproval(t *testing.T) {
	var status int
	var code string
	handler := NewNotificationsHandler(NotificationsDependencies{
		Principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reviewer"}}, accessfixture.Bundle{Permissions: []string{PermissionTemplateApprove}})
		},
		WriteError: func(_ http.ResponseWriter, _ *http.Request, value int, valueCode string, _ ...string) {
			status, code = value, valueCode
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/notifications/templates/order-ready/publish", nil)
	request.SetPathValue("templateKey", "order-ready")
	handler.publish(httptest.NewRecorder(), request)

	if status != http.StatusConflict || code != "backend.notification.publication_request_required" {
		t.Fatalf("legacy direct publish = (%d, %q), want (%d, %q)", status, code, http.StatusConflict, "backend.notification.publication_request_required")
	}
}

func TestNotificationTemplateRoutesAreOwnedByModuleSurface(t *testing.T) {
	handler, capture, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/notifications/templates", nil))
	if response.Code != http.StatusNotFound || capture.status != 0 {
		t.Fatalf("legacy template route = (%d, %d, %v), want module-owned 404", response.Code, capture.status, capture.err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/notifications/templates/order-ready", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("method route status=%d", response.Code)
	}
}
