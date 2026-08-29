package workflows

import (
	"context"
	"net/http"
	"testing"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowAuthoringFragmentValidationHandler(t *testing.T) {
	handler, _, response := newWorkflowHTTPDefinitionFixture()
	writer, request := workflowHTTPRequest(http.MethodPost, "/workflows/authoring-fragments/workflow.trigger_contract/validate", `{"type":"manual"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	result, ok := response.value.(workflowmodel.WorkflowValidation)
	if response.status != http.StatusOK || !ok || !result.Valid || len(result.Issues) != 0 || response.err != nil {
		t.Fatalf("status=%d result=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflows/authoring-fragments/workflow.trigger_contract/validate", `{"type":"unknown"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	result, ok = response.value.(workflowmodel.WorkflowValidation)
	if response.status != http.StatusOK || !ok || result.Valid || len(result.Issues) != 1 || result.Issues[0].CapabilityKey != "workflow.trigger_contract" || result.Issues[0].Code != "backend.workflow.trigger_type_invalid" {
		t.Fatalf("status=%d result=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflows/authoring-fragments/workflow.trigger_contract/validate", `{`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
	handler.validateAuthoringFragment(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
		t.Fatalf("invalid json status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	handler.principal = func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} }
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflows/authoring-fragments/workflow.trigger_contract/validate", `{"type":"manual"}`, map[string]string{"capabilityKey": "workflow.trigger_contract"})
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

func TestWorkflowExecutionListAndProcessHandlers(t *testing.T) {
	handler, _, workers, _, response := newWorkflowHTTPRuntimeFixture()
	workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", WorkspaceID: "workspace-1", WorkflowKey: "order.approve", Status: "completed", Payload: map[string]any{"object_key": "order", "record_id": "record-1"}}
	writer, request := workflowHTTPRequest(http.MethodGet, "/workflow-executions?limit=25", "", nil)
	handler.listWorkflowExecutions(writer, request)
	if response.status != http.StatusOK || response.value == nil || response.err != nil {
		t.Fatalf("list status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow-executions/process?limit=10", "", nil)
	handler.processWorkflowExecutions(writer, request)
	if response.status != http.StatusOK || response.value == nil || response.err != nil {
		t.Fatalf("process status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	workers.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodGet, "/workflow-executions", "", nil)
	handler.listWorkflowExecutions(writer, request)
	if response.err == nil || response.value != nil {
		t.Fatalf("list error value=%#v err=%v", response.value, response.err)
	}
	workers.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow-executions/process", "", nil)
	handler.processWorkflowExecutions(writer, request)
	if response.err == nil || response.value != nil {
		t.Fatalf("process error value=%#v err=%v", response.value, response.err)
	}
}

func TestWorkflowExecutionRetryAndResolveHandlers(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		handler, _, workers, _, response := newWorkflowHTTPRuntimeFixture()
		workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", WorkspaceID: "workspace-1", WorkflowKey: "order.approve", Status: "failed", Attempt: 1, Result: map[string]any{}}
		writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-executions/execution-1/retry", "", map[string]string{"executionID": " execution-1 "})
		request.Header.Set("Idempotency-Key", " retry-1 ")
		handler.retryWorkflowExecution(writer, request)
		if response.status != http.StatusOK || response.value == nil || response.err != nil {
			t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
		}

		resetWorkflowHTTPResponse(response)
		writer, request = workflowHTTPRequest(http.MethodPost, "/workflow-executions/execution-1/retry", "", map[string]string{"executionID": "execution-1"})
		handler.retryWorkflowExecution(writer, request)
		if response.status != http.StatusBadRequest || response.code == "" {
			t.Fatalf("missing key status=%d code=%q", response.status, response.code)
		}
	})

	t.Run("resolve with and without body", func(t *testing.T) {
		for _, body := range []string{"", `{"reason":"operator verified"}`} {
			handler, _, workers, _, response := newWorkflowHTTPRuntimeFixture()
			workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", WorkspaceID: "workspace-1", WorkflowKey: "order.approve", Status: "dead_letter", Result: map[string]any{}}
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-executions/execution-1/resolve", body, map[string]string{"executionID": " execution-1 "})
			handler.resolveWorkflowExecution(writer, request)
			if response.status != http.StatusOK || response.value == nil || response.err != nil {
				t.Fatalf("body=%q status=%d value=%#v err=%v", body, response.status, response.value, response.err)
			}
		}
	})

	t.Run("resolve invalid json", func(t *testing.T) {
		handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
		writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-executions/execution-1/resolve", `{`, map[string]string{"executionID": "execution-1"})
		handler.resolveWorkflowExecution(writer, request)
		if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
			t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
		}
	})
}

func TestWorkflowExecuteOwnerOperationUsesOperationsReceipt(t *testing.T) {
	handler, _, _, _, _ := newWorkflowHTTPRuntimeFixture()
	repository := &workflowHTTPOperationsRepository{receipts: map[string]operationsmodel.OperationsReceipt{}}
	handler.operations = operationsapplication.NewOperationsApplicationService(repository, nil, nil, func() string { return "workflow-http" })
	_, request := workflowHTTPRequest(http.MethodPost, "/workflow-processes/process-1/cancel", "", nil)
	request.Header.Set("Idempotency-Key", "cancel-1")
	request.Header.Set("X-Operation-Reason", "operator verified cancellation")
	request.Header.Set("X-Operation-Reference", "incident-1")
	result, err := handler.executeOwnerOperation(request, "workflow.process.cancel", "workflow_process", "process-1", map[string]any{"requested": true}, func(context.Context) (any, error) {
		return map[string]any{"status": "cancelled"}, nil
	})
	if err != nil || result.Value == nil || result.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestWorkflowRunAndSimulationHTTPHandlers(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		paths map[string]string
		call  func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "run", body: `{"payload":{"order_id":"order-1"}}`, paths: map[string]string{"workflowKey": " order.approve "}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.runWorkflow(writer, request)
		}},
		{name: "simulate", body: `{"payload":{"order_id":"order-1"}}`, paths: map[string]string{"workflowKey": " order.approve "}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.simulateWorkflow(writer, request)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", test.body, test.paths)
			test.call(handler, writer, request)
			if response.status != http.StatusOK || response.value == nil || response.err != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}

	for _, test := range []struct {
		name  string
		paths map[string]string
		call  func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "run", paths: map[string]string{"workflowKey": "order.approve"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.runWorkflow(writer, request)
		}},
		{name: "simulate", paths: map[string]string{"workflowKey": "order.approve"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.simulateWorkflow(writer, request)
		}},
	} {
		t.Run(test.name+" invalid json", func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", `{`, test.paths)
			test.call(handler, writer, request)
			if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}
}

func TestWorkflowOptionalBodiesMayBeNilOrEmpty(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "resolve", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.resolveWorkflowExecution(writer, request)
		}},
		{name: "run", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.runWorkflow(writer, request)
		}},
		{name: "simulate", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.simulateWorkflow(writer, request)
		}},
	} {
		t.Run(test.name+" nil", func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", "", map[string]string{"executionID": "missing", "workflowKey": "missing"})
			request.Body = nil
			test.call(handler, writer, request)
			if response.status == http.StatusBadRequest {
				t.Fatalf("nil optional body was decoded: status=%d error=%v", response.status, response.err)
			}
		})
		t.Run(test.name+" empty", func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", "", map[string]string{"executionID": "missing", "workflowKey": "missing"})
			test.call(handler, writer, request)
			if response.status == http.StatusBadRequest {
				t.Fatalf("empty optional body was decoded: status=%d error=%v", response.status, response.err)
			}
		})
	}
}

func TestWorkflowExecutionHandlersPropagateServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		paths map[string]string
		key   bool
		call  func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "retry", paths: map[string]string{"executionID": "missing"}, key: true, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.retryWorkflowExecution(writer, request)
		}},
		{name: "resolve", paths: map[string]string{"executionID": "missing"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.resolveWorkflowExecution(writer, request)
		}},
		{name: "run", paths: map[string]string{"workflowKey": "missing"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.runWorkflow(writer, request)
		}},
		{name: "simulate", paths: map[string]string{"workflowKey": "missing"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.simulateWorkflow(writer, request)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", test.body, test.paths)
			if test.key {
				request.Header.Set("Idempotency-Key", "key-1")
			}
			test.call(handler, writer, request)
			if response.err == nil || response.value != nil {
				t.Fatalf("value=%#v err=%v", response.value, response.err)
			}
		})
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
