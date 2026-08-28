package integrations

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestRevokeWebPushSubscriptionRequiresIdempotencyKeyBeforeOwnerMutation(t *testing.T) {
	handler := &IntegrationsHandler{writeServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
		if apperror.CodeOf(err) != "backend.idempotency.key_required" {
			t.Fatalf("error=%v", err)
		}
		w.WriteHeader(http.StatusBadRequest)
	}}
	response := httptest.NewRecorder()
	handler.revokeWebPushSubscription(response, httptest.NewRequest(http.MethodPost, "/business/notifications/web-push/subscriptions/sub/revoke", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}
