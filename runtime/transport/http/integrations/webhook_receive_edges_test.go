package integrations

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type failingWebhookBody struct{}

func (failingWebhookBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingWebhookBody) Close() error             { return nil }

func TestReceiveIntegrationWebhookRejectsUnreadableBody(t *testing.T) {
	status, code := 0, ""
	handler := &IntegrationsHandler{writeError: func(_ http.ResponseWriter, _ *http.Request, value int, errorCode string, _ ...string) {
		status, code = value, errorCode
	}}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = failingWebhookBody{}
	handler.receiveIntegrationWebhook(httptest.NewRecorder(), request)
	if status != http.StatusBadRequest || code != "backend.integration.webhook.body_invalid" {
		t.Fatalf("status=%d code=%q", status, code)
	}
	if values := cloneHTTPValues(map[string][]string{"empty": nil, "value": {"first", "second"}}); len(values) != 2 || !reflect.DeepEqual(values["value"], []string{"first", "second"}) {
		t.Fatalf("first values = %#v", values)
	}
	var _ io.ReadCloser = failingWebhookBody{}
}
