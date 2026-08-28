package workflows

import (
	"context"
	"net/http"
	"testing"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowSurfaceReadHandlersAndErrors(t *testing.T) {
	handler, processes, workers, scheduler, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")
	workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", Status: "completed"}
	scheduler.result = workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "processed-1", Status: "completed"}}}

	tests := []struct {
		name   string
		target string
		paths  map[string]string
		call   func(http.ResponseWriter, *http.Request)
		fail   func()
	}{
		{name: "business tasks", target: "/business/workflow/tasks?status=open&limit=10", call: handler.listBusinessWorkflowTasks, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "business team tasks", target: "/business/workflow/team-tasks?status=open&limit=10", call: handler.listBusinessTeamWorkflowTasks, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "business processes", target: "/business/workflow/processes?status=waiting,running&limit=10", call: handler.listBusinessWorkflowProcesses, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "business process", target: "/business/workflow/processes/process-1", paths: map[string]string{"processID": " process-1 "}, call: handler.getBusinessWorkflowProcess, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "ops executions", target: "/operations/workflow/executions?limit=10", call: handler.listOpsWorkflowExecutions, fail: func() { workers.err = errWorkflowHTTPTest }},
		{name: "ops processes", target: "/operations/workflow/processes?status=waiting&limit=10", call: handler.listOpsWorkflowProcesses, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "ops process", target: "/operations/workflow/processes/process-1", paths: map[string]string{"processID": " process-1 "}, call: handler.getOpsWorkflowProcess, fail: func() { processes.err = errWorkflowHTTPTest }},
		{name: "process executions", target: "/operations/workflow/executions/process?limit=10", call: handler.processOpsWorkflowExecutions, fail: func() { scheduler.err = errWorkflowHTTPTest }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processes.err, workers.err, scheduler.err = nil, nil, nil
			resetWorkflowHTTPResponse(response)
			writer, request := workflowHTTPRequest(http.MethodGet, test.target, "", test.paths)
			test.call(writer, request)
			if response.status != http.StatusOK || response.err != nil {
				t.Fatalf("success status=%d value=%#v err=%v", response.status, response.value, response.err)
			}

			test.fail()
			resetWorkflowHTTPResponse(response)
			writer, request = workflowHTTPRequest(http.MethodGet, test.target, "", test.paths)
			test.call(writer, request)
			if response.err == nil || response.value != nil {
				t.Fatalf("error value=%#v err=%v", response.value, response.err)
			}
		})
	}
}

func TestBusinessWorkflowSurfaceCommands(t *testing.T) {
	for _, decision := range []struct {
		name string
		call func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "approve", call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.approveBusinessWorkflowTask(w, r) }},
		{name: "reject", call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.rejectBusinessWorkflowTask(w, r) }},
		{name: "return", call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.returnBusinessWorkflowTask(w, r) }},
	} {
		t.Run(decision.name, func(t *testing.T) {
			handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
			seedWorkflowHTTPProcess(processes, "waiting")
			writer, request := workflowHTTPRequest(http.MethodPost, "/business/workflow/tasks/task-1/"+decision.name, `{"comment":"reviewed"}`, map[string]string{"taskID": " task-1 "})
			request.Header.Set("Idempotency-Key", "decision-1")
			decision.call(handler, writer, request)
			if response.status != http.StatusOK || response.err != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}

	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")
	writer, request := workflowHTTPRequest(http.MethodPost, "/business/workflow/tasks/task-1/approve", "", map[string]string{"taskID": "task-1"})
	handler.approveBusinessWorkflowTask(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("missing key status=%d", response.status)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/tasks/task-1/approve", `{`, map[string]string{"taskID": "task-1"})
	request.Header.Set("Idempotency-Key", "decision-2")
	handler.approveBusinessWorkflowTask(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil {
		t.Fatalf("invalid body status=%d err=%v", response.status, response.err)
	}

	processes.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/tasks/task-1/approve", "", map[string]string{"taskID": "task-1"})
	request.Header.Set("Idempotency-Key", "decision-3")
	handler.approveBusinessWorkflowTask(writer, request)
	if response.err == nil {
		t.Fatal("decision service error not propagated")
	}
}

func TestBusinessWorkflowWithdrawAndRunEdges(t *testing.T) {
	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")
	writer, request := workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/withdraw", "", map[string]string{"processID": " process-1 "})
	request.Header.Set("Idempotency-Key", "withdraw-1")
	handler.withdrawBusinessWorkflowProcess(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("withdraw status=%d err=%v", response.status, response.err)
	}

	handler, processes, _, _, response = newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")
	processes.err = errWorkflowHTTPTest
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/withdraw", "", map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "withdraw-2")
	handler.withdrawBusinessWorkflowProcess(writer, request)
	if response.err == nil {
		t.Fatal("withdraw service error not propagated")
	}

	handler, processes, _, _, response = newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "failed")
	process := processes.processes["process-1"]
	process.DefinitionSnapshot.Graph.Nodes[1] = definitionmodel.WorkflowGraphNode{
		ID: "approval", Type: "condition", Config: map[string]any{"expression": "true"},
	}
	processes.processes["process-1"] = process
	nodes := processes.nodes["process-1"]
	nodes[0].NodeType, nodes[0].Status = "condition", "failed"
	processes.nodes["process-1"] = nodes
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/retry", "", map[string]string{"processID": " process-1 "})
	request.Header.Set("Idempotency-Key", "retry-1")
	handler.retryBusinessWorkflowProcess(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("retry status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/retry", "", map[string]string{"processID": "process-1"})
	handler.retryBusinessWorkflowProcess(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("retry missing key status=%d", response.status)
	}
	processes.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/retry", "", map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "retry-failure")
	handler.retryBusinessWorkflowProcess(writer, request)
	if response.err == nil {
		t.Fatal("retry service error not propagated")
	}

	handler, _, _, _, response = newWorkflowHTTPRuntimeFixture()
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflows/order.approve/run", `{"payload":{"order_id":"order-1"}}`, map[string]string{"workflowKey": " order.approve "})
	request.Header.Set("Idempotency-Key", "run-1")
	handler.runBusinessWorkflow(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("run status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflows/order.approve/run", "", map[string]string{"workflowKey": "order.approve"})
	handler.runBusinessWorkflow(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("run missing key status=%d", response.status)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflows/order.approve/run", `{`, map[string]string{"workflowKey": "order.approve"})
	request.Header.Set("Idempotency-Key", "run-2")
	handler.runBusinessWorkflow(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil {
		t.Fatalf("run invalid body status=%d err=%v", response.status, response.err)
	}
}

type workflowHTTPReplayOperationsRepository struct {
	*workflowHTTPOperationsRepository
}

func (r *workflowHTTPReplayOperationsRepository) RegisterOperationsCommand(_ context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	receipt.Command.Status = operationsmodel.OperationsStatusSucceeded
	receipt.Result = []byte(`{}`)
	return receipt, operationsmodel.OperationsSubmissionReplay, nil
}

func workflowHTTPReplayOperationsService() *operationsapplication.OperationsApplicationService {
	repository := &workflowHTTPReplayOperationsRepository{workflowHTTPOperationsRepository: &workflowHTTPOperationsRepository{receipts: map[string]operationsmodel.OperationsReceipt{}}}
	return operationsapplication.NewOperationsApplicationService(repository, nil, nil, func() string { return "workflow-replay" })
}

func TestOpsWorkflowCommandEdges(t *testing.T) {
	handler, _, workers, _, response := newWorkflowHTTPRuntimeFixture()
	workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", WorkspaceID: "workspace-1", WorkflowKey: "order.approve", Status: "failed", Result: map[string]any{}}
	writer, request := workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/retry", "", map[string]string{"executionID": " execution-1 "})
	request.Header.Set("Idempotency-Key", "retry-1")
	handler.retryOpsWorkflowExecution(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("retry status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	handler.operations = workflowHTTPReplayOperationsService()
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/retry", "", map[string]string{"executionID": "execution-1"})
	request.Header.Set("Idempotency-Key", "retry-replay")
	handler.retryOpsWorkflowExecution(writer, request)
	if response.status != http.StatusInternalServerError {
		t.Fatalf("retry replay projection status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	handler, _, workers, _, response = newWorkflowHTTPRuntimeFixture()
	workers.executions["execution-1"] = workflowmodel.WorkflowExecution{ID: "execution-1", WorkspaceID: "workspace-1", WorkflowKey: "order.approve", Status: "dead_letter", Result: map[string]any{}}
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/resolve", `{"reason":"verified"}`, map[string]string{"executionID": " execution-1 "})
	request.Header.Set("Idempotency-Key", "resolve-1")
	handler.resolveOpsWorkflowExecution(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("resolve status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	handler.operations = workflowHTTPReplayOperationsService()
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/resolve", "", map[string]string{"executionID": "execution-1"})
	request.Header.Set("Idempotency-Key", "resolve-replay")
	handler.resolveOpsWorkflowExecution(writer, request)
	if response.status != http.StatusInternalServerError {
		t.Fatalf("resolve replay projection status=%d", response.status)
	}

	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "configuration_error")
	handler.operations = workflowHTTPReplayOperationsService()
	for _, command := range []struct {
		name string
		body string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "retry", call: handler.retryOpsWorkflowProcess},
		{name: "resolve", body: `{"note":"fixed"}`, call: handler.resolveOpsWorkflowProcess},
	} {
		resetWorkflowHTTPResponse(response)
		writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/"+command.name, command.body, map[string]string{"processID": "process-1"})
		request.Header.Set("Idempotency-Key", command.name+"-replay")
		command.call(writer, request)
		if response.status != http.StatusInternalServerError {
			t.Fatalf("%s replay projection status=%d value=%#v err=%v", command.name, response.status, response.value, response.err)
		}
	}
}

func TestWorkflowSurfaceRemainingFailureEdges(t *testing.T) {
	handler, processes, workers, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")

	writer, request := workflowHTTPRequest(http.MethodPost, "/business/workflow/processes/process-1/withdraw", "", map[string]string{"processID": "process-1"})
	handler.withdrawBusinessWorkflowProcess(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("withdraw missing key status=%d", response.status)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflows/order.approve/run", "", map[string]string{"workflowKey": "order.approve"})
	request.Header.Set("Idempotency-Key", "run-empty")
	handler.runBusinessWorkflow(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("empty run status=%d err=%v", response.status, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/business/workflows/missing/run", "", map[string]string{"workflowKey": "missing"})
	request.Body = nil
	request.Header.Set("Idempotency-Key", "run-missing")
	handler.runBusinessWorkflow(writer, request)
	if response.err == nil {
		t.Fatal("run service error not propagated")
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/retry", "", map[string]string{"executionID": "execution-1"})
	handler.retryOpsWorkflowExecution(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("retry missing key status=%d", response.status)
	}

	workers.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/retry", "", map[string]string{"executionID": "execution-1"})
	request.Header.Set("Idempotency-Key", "retry-error")
	handler.retryOpsWorkflowExecution(writer, request)
	if response.err == nil {
		t.Fatal("retry service error not propagated")
	}

	workers.err = nil
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/resolve", "", map[string]string{"executionID": "execution-1"})
	handler.resolveOpsWorkflowExecution(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("resolve missing key status=%d", response.status)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/resolve", `{`, map[string]string{"executionID": "execution-1"})
	request.Header.Set("Idempotency-Key", "resolve-invalid")
	handler.resolveOpsWorkflowExecution(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil {
		t.Fatalf("resolve invalid body status=%d err=%v", response.status, response.err)
	}

	workers.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/executions/execution-1/resolve", "", map[string]string{"executionID": "execution-1"})
	request.Body = nil
	request.Header.Set("Idempotency-Key", "resolve-error")
	handler.resolveOpsWorkflowExecution(writer, request)
	if response.err == nil {
		t.Fatal("resolve service error not propagated")
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/retry", "", map[string]string{"processID": "process-1"})
	handler.retryOpsWorkflowProcess(writer, request)
	if response.status != http.StatusBadRequest {
		t.Fatalf("process retry missing key status=%d", response.status)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/resolve", `{`, map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "process-resolve-invalid")
	handler.resolveOpsWorkflowProcess(writer, request)
	if response.status != http.StatusBadRequest || response.err == nil {
		t.Fatalf("process resolve invalid status=%d err=%v", response.status, response.err)
	}

	handler, processes, _, _, response = newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "failed")
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/retry", "", map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "process-retry")
	handler.retryOpsWorkflowProcess(writer, request)
	if response.err == nil {
		t.Fatal("process retry fixture should expose continuation error")
	}

	processes.err = errWorkflowHTTPTest
	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/resolve", `{"note":"fixed"}`, map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "process-resolve-error")
	handler.resolveOpsWorkflowProcess(writer, request)
	if response.err == nil {
		t.Fatal("process resolve service error not propagated")
	}
}
