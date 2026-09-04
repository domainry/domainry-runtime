package workflows

import (
	"context"
	"net/http"
	"testing"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowAuthoringFragmentValidationHandler(t *testing.T) {
	handler, _, response := newWorkflowHTTPDefinitionFixture()
	writer, request := workflowHTTPRequest(http.MethodPost, "/workflow/authoring-fragments/workflow.trigger_contract/validate", `{"type":"manual"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	result, ok := response.value.(workflowmodel.WorkflowValidation)
	if response.status != http.StatusOK || !ok || !result.Valid || result.CapabilityKey != "workflow.trigger_contract" || result.Fragment["type"] != "manual" || len(result.Issues) != 0 || response.err != nil {
		t.Fatalf("status=%d result=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/authoring-fragments/workflow.trigger_contract/validate", `{"type":"unknown"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	result, ok = response.value.(workflowmodel.WorkflowValidation)
	if response.status != http.StatusOK || !ok || result.Valid || len(result.Issues) != 1 || result.Issues[0].CapabilityKey != "workflow.trigger_contract" || result.Issues[0].Code != "backend.workflow.trigger_type_invalid" {
		t.Fatalf("status=%d result=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/authoring-fragments/workflow.trigger_contract/validate", `{`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
		t.Fatalf("invalid json status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	handler.principal = func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} }
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/authoring-fragments/workflow.trigger_contract/validate", `{"type":"manual"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	if response.err == nil || response.value != nil {
		t.Fatalf("authorization value=%#v err=%v", response.value, response.err)
	}
}

func TestWorkflowRegisterRoutes(t *testing.T) {
	handler, _, _ := newWorkflowHTTPDefinitionFixture()
	handler.RegisterRoutes(http.NewServeMux())
}

func TestWorkflowHTTPIntQuery(t *testing.T) {
	if got := intQuery(" 42 "); got != 42 {
		t.Fatalf("intQuery=%d", got)
	}
	if got := intQuery("invalid"); got != 0 {
		t.Fatalf("invalid intQuery=%d", got)
	}
}

func TestWorkflowSimulationHTTPHandler(t *testing.T) {
	handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
	writer, request := workflowHTTPRequest(http.MethodPost, "/workflow/definitions/order.approve/simulate", `{"payload":{"order_id":"order-1"}}`, map[string]string{"workflowKey": " order.approve "})
	handler.simulateWorkflow(writer, request)
	if response.status != http.StatusOK || response.value == nil || response.err != nil {
		t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/definitions/order.approve/simulate", `{`, map[string]string{"workflowKey": "order.approve"})
	handler.simulateWorkflow(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
		t.Fatalf("invalid JSON status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/definitions/missing/simulate", "", map[string]string{"workflowKey": "missing"})
	request.Body = nil
	handler.simulateWorkflow(writer, request)
	if response.err == nil || response.value != nil {
		t.Fatalf("missing workflow value=%#v err=%v", response.value, response.err)
	}
}

type workflowHTTPOperationsRepository struct {
	receipts map[string]operationsmodel.OperationsReceipt
}

func (r *workflowHTTPOperationsRepository) RegisterOperationsCommand(_ context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	r.receipts[receipt.Command.ID] = receipt
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}

func (r *workflowHTTPOperationsRepository) GetOperationsReceipt(_ context.Context, scope operationsmodel.OperationsScope, id string) (operationsmodel.OperationsReceipt, bool, error) {
	receipt, found := r.receipts[id]
	if found && receipt.Command.Scope.WorkspaceID != scope.WorkspaceID {
		return operationsmodel.OperationsReceipt{}, false, nil
	}
	return receipt, found, nil
}

func (r *workflowHTTPOperationsRepository) ListOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsStatus, int) ([]operationsmodel.OperationsReceipt, error) {
	return nil, nil
}

func (r *workflowHTTPOperationsRepository) SearchOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	return operationsmodel.OperationsReceiptPage{Items: []operationsmodel.OperationsReceipt{}}, nil
}

func (r *workflowHTTPOperationsRepository) UpdateOperationsReceipt(_ context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	current, found := r.receipts[receipt.Command.ID]
	if !found || current.Command.Status != expected {
		return false, nil
	}
	r.receipts[receipt.Command.ID] = receipt
	return true, nil
}
