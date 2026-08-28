package workflows

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestValidateWorkflowDefinitionHandlerSuccessAndEdges(t *testing.T) {
	handler, _, response := newWorkflowHTTPDefinitionFixture()
	request := httptest.NewRequest(http.MethodPost, "/workflows/order.approve/validate", bytes.NewBufferString(`{"payload":{"key":"order.approve","name":"Order approve","trigger_contract":{"type":"manual"},"graph":{"version":2,"nodes":[{"id":"start","type":"trigger","name":"Start"}]}}}`))
	request.SetPathValue("workflowKey", "order.approve")
	handler.validateWorkflowDefinition(httptest.NewRecorder(), request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("response=%#v", response)
	}

	response.status, response.code, response.err = 0, "", nil
	mismatch := httptest.NewRequest(http.MethodPost, "/workflows/order.approve/validate", bytes.NewBufferString(`{"payload":{"key":"other"}}`))
	mismatch.SetPathValue("workflowKey", "order.approve")
	handler.validateWorkflowDefinition(httptest.NewRecorder(), mismatch)
	if response.status != http.StatusBadRequest || response.code != "backend.workflow.definition_identity_required" {
		t.Fatalf("mismatch response=%#v", response)
	}
	response.status, response.code, response.err = 0, "", nil
	blank := httptest.NewRequest(http.MethodPost, "/workflows/validate", bytes.NewBufferString(`{"payload":{"key":"other"}}`))
	handler.validateWorkflowDefinition(httptest.NewRecorder(), blank)
	if response.status != http.StatusBadRequest || response.code != "backend.workflow.definition_identity_required" {
		t.Fatalf("blank key response=%#v", response)
	}

	response.status, response.code, response.err = 0, "", nil
	handler.principal = func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} }
	unauthorized := httptest.NewRequest(http.MethodPost, "/workflows/order.approve/validate", bytes.NewBufferString(`{"payload":{"key":"order.approve"}}`))
	unauthorized.SetPathValue("workflowKey", "order.approve")
	handler.validateWorkflowDefinition(httptest.NewRecorder(), unauthorized)
	if response.err == nil {
		t.Fatalf("unauthorized response=%#v", response)
	}
}

func TestValidateWorkflowDefinitionHandlerStopsOnDecodeFailure(t *testing.T) {
	handler, _, response := newWorkflowHTTPDefinitionFixture()
	handler.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return false }
	handler.validateWorkflowDefinition(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if response.status != 0 || response.err != nil {
		t.Fatalf("response=%#v", response)
	}
}
