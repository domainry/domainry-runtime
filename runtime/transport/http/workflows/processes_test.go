package workflows

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestWorkflowProcessReadAndWorkerHandlers(t *testing.T) {
	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "waiting")
	for _, test := range []struct {
		name   string
		target string
		paths  map[string]string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{name: "list processes", target: "/workflow-processes?workflow_key=order.approve&definition_version=2&object_key=&record_id=&status=waiting&initiator_id=admin-1&approver_id=admin-1&updated_from=2026-01-01&updated_to=2026-12-31&limit=25", call: handler.listWorkflowProcesses},
		{name: "my tasks", target: "/workflow-tasks?status=open&limit=20", call: handler.listMyWorkflowTasks},
		{name: "get process", target: "/workflow-processes/process-1", paths: map[string]string{"processID": " process-1 "}, call: handler.getWorkflowProcess},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetWorkflowHTTPResponse(response)
			writer, request := workflowHTTPRequest(http.MethodGet, test.target, "", test.paths)
			test.call(writer, request)
			if response.status != http.StatusOK || response.value == nil || response.err != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}
}

func TestOpsWorkflowProcessListAndSafeDetailHandlers(t *testing.T) {
	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "failed")

	writer, request := workflowHTTPRequest(http.MethodGet, "/operations/workflow/processes?status=failed&status=configuration_error&resource_id=process-1&limit=25", "", nil)
	handler.listOpsWorkflowProcesses(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("list status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodGet, "/operations/workflow/processes/process-1", "", map[string]string{"processID": " process-1 "})
	handler.getOpsWorkflowProcess(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("detail status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	seedWorkflowHTTPProcess(processes, "configuration_error")
	writer, request = workflowHTTPRequest(http.MethodPost, "/operations/workflow/processes/process-1/resolve", `{"note":"configuration repaired"}`, map[string]string{"processID": "process-1"})
	request.Header.Set("Idempotency-Key", "ops-resolve-1")
	handler.resolveOpsWorkflowProcess(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("resolve status=%d value=%#v err=%v", response.status, response.value, response.err)
	}
	raw, err := json.Marshal(response.value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"definition_snapshot", `"variables"`, `"result"`, `"tasks"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("Ops recovery response leaked %s: %s", forbidden, raw)
		}
	}
}

func TestWorkflowTaskDecisionHandlers(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "approve", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.approveWorkflowTask(writer, request)
		}},
		{name: "reject", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.rejectWorkflowTask(writer, request)
		}},
		{name: "return", call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.returnWorkflowTask(writer, request)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
			seedWorkflowHTTPProcess(processes, "waiting")
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-tasks/task-1/"+test.name, `{"comment":"reviewed"}`, map[string]string{"taskID": " task-1 "})
			request.Header.Set("Idempotency-Key", " decision-1 ")
			test.call(handler, writer, request)
			if response.status != http.StatusOK || response.value == nil || response.err != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}
}

func TestWorkflowProcessOwnerCommands(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
		seedWorkflowHTTPProcess(processes, "waiting")
		writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-processes/process-1/cancel", "", map[string]string{"processID": " process-1 "})
		request.Header.Set("Idempotency-Key", " cancel-1 ")
		handler.cancelWorkflowProcess(writer, request)
		if response.status != http.StatusOK || response.value == nil || response.err != nil || processes.processes["process-1"].Status != "cancelled" {
			t.Fatalf("status=%d value=%#v process=%#v err=%v", response.status, response.value, processes.processes["process-1"], response.err)
		}
	})

	t.Run("retry", func(t *testing.T) {
		handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
		seedWorkflowHTTPProcess(processes, "failed")
		processes.nodes["process-1"][0].NodeID = "start"
		processes.nodes["process-1"][0].NodeType = "trigger"
		processes.nodes["process-1"][0].Status = "failed"
		process := processes.processes["process-1"]
		process.DefinitionSnapshot.Graph.Nodes = process.DefinitionSnapshot.Graph.Nodes[:1]
		process.DefinitionSnapshot.Graph.Edges = nil
		process.CurrentNodeIDs = []string{"start"}
		processes.processes[process.ID] = process
		writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-processes/process-1/retry", "", map[string]string{"processID": " process-1 "})
		request.Header.Set("Idempotency-Key", " retry-1 ")
		handler.retryWorkflowProcess(writer, request)
		if response.err == nil {
			t.Fatal("first retry should expose the fixture's unsupported trigger continuation")
		}
		resetWorkflowHTTPResponse(response)
		writer, request = workflowHTTPRequest(http.MethodPost, "/workflow-processes/process-1/retry", "", map[string]string{"processID": " process-1 "})
		request.Header.Set("Idempotency-Key", " retry-1 ")
		handler.retryWorkflowProcess(writer, request)
		if response.status != http.StatusOK || response.value == nil || response.err != nil {
			t.Fatalf("status=%d value=%#v process=%#v err=%v", response.status, response.value, processes.processes["process-1"], response.err)
		}
	})

	t.Run("resolve", func(t *testing.T) {
		handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
		seedWorkflowHTTPProcess(processes, "configuration_error")
		writer, request := workflowHTTPRequest(http.MethodPost, "/workflow-processes/process-1/resolve", `{"note":"configuration repaired"}`, map[string]string{"processID": " process-1 "})
		handler.resolveWorkflowProcessFailure(writer, request)
		if response.status != http.StatusOK || response.value == nil || response.err != nil || processes.processes["process-1"].Status != "resolved" {
			t.Fatalf("status=%d value=%#v process=%#v err=%v", response.status, response.value, processes.processes["process-1"], response.err)
		}
	})
}

func TestWorkflowProcessHandlersRejectMissingKeysAndInvalidJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths map[string]string
		call  func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "task decision", paths: map[string]string{"taskID": "task-1"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.approveWorkflowTask(writer, request)
		}},
		{name: "cancel", paths: map[string]string{"processID": "process-1"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.cancelWorkflowProcess(writer, request)
		}},
		{name: "retry", paths: map[string]string{"processID": "process-1"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.retryWorkflowProcess(writer, request)
		}},
	} {
		t.Run(test.name+" missing key", func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", "", test.paths)
			test.call(handler, writer, request)
			if response.status != http.StatusBadRequest || response.code == "" || response.value != nil {
				t.Fatalf("status=%d code=%q value=%#v", response.status, response.code, response.value)
			}
		})
	}

	for _, test := range []struct {
		name  string
		paths map[string]string
		call  func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "task decision", paths: map[string]string{"taskID": "task-1"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.approveWorkflowTask(writer, request)
		}},
		{name: "resolve", paths: map[string]string{"processID": "process-1"}, call: func(handler *WorkflowsHandler, writer http.ResponseWriter, request *http.Request) {
			handler.resolveWorkflowProcessFailure(writer, request)
		}},
	} {
		t.Run(test.name+" invalid json", func(t *testing.T) {
			handler, _, _, _, response := newWorkflowHTTPRuntimeFixture()
			writer, request := workflowHTTPRequest(http.MethodPost, "/workflow", `{`, test.paths)
			request.Header.Set("Idempotency-Key", "key-1")
			test.call(handler, writer, request)
			if response.status != http.StatusBadRequest || response.err == nil || response.value != nil {
				t.Fatalf("status=%d value=%#v err=%v", response.status, response.value, response.err)
			}
		})
	}
}

func TestWorkflowProcessHandlersPropagateServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		body   string
		paths  map[string]string
		key    bool
		call   func(*WorkflowsHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "list", target: "/workflow-processes", call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.listWorkflowProcesses(w, r) }},
		{name: "tasks", target: "/workflow-tasks", call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.listMyWorkflowTasks(w, r) }},
		{name: "decision", target: "/workflow-tasks/task-1/approve", paths: map[string]string{"taskID": "task-1"}, key: true, call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.approveWorkflowTask(w, r) }},
		{name: "get", target: "/workflow-processes/process-1", paths: map[string]string{"processID": "process-1"}, call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.getWorkflowProcess(w, r) }},
		{name: "cancel", target: "/workflow-processes/process-1/cancel", paths: map[string]string{"processID": "process-1"}, key: true, call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.cancelWorkflowProcess(w, r) }},
		{name: "retry", target: "/workflow-processes/process-1/retry", paths: map[string]string{"processID": "process-1"}, key: true, call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) { h.retryWorkflowProcess(w, r) }},
		{name: "resolve", target: "/workflow-processes/process-1/resolve", body: `{"note":"fixed"}`, paths: map[string]string{"processID": "process-1"}, call: func(h *WorkflowsHandler, w http.ResponseWriter, r *http.Request) {
			h.resolveWorkflowProcessFailure(w, r)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, processes, workers, _, response := newWorkflowHTTPRuntimeFixture()
			seedWorkflowHTTPProcess(processes, "failed")
			processes.err, workers.err = errWorkflowHTTPTest, errWorkflowHTTPTest
			writer, request := workflowHTTPRequest(http.MethodPost, test.target, test.body, test.paths)
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
