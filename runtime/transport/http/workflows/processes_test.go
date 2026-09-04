package workflows

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestOpsWorkflowProcessListAndSafeDetailHandlers(t *testing.T) {
	handler, processes, _, _, response := newWorkflowHTTPRuntimeFixture()
	seedWorkflowHTTPProcess(processes, "failed")

	writer, request := workflowHTTPRequest(http.MethodGet, "/workflow/recovery/processes?status=failed&status=configuration_error&resource_id=process-1&limit=25", "", nil)
	handler.listOpsWorkflowProcesses(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("list status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	writer, request = workflowHTTPRequest(http.MethodGet, "/workflow/recovery/processes/process-1", "", map[string]string{"processID": " process-1 "})
	handler.getOpsWorkflowProcess(writer, request)
	if response.status != http.StatusOK || response.err != nil {
		t.Fatalf("detail status=%d value=%#v err=%v", response.status, response.value, response.err)
	}

	resetWorkflowHTTPResponse(response)
	seedWorkflowHTTPProcess(processes, "configuration_error")
	writer, request = workflowHTTPRequest(http.MethodPost, "/workflow/recovery/processes/process-1/resolve", `{"note":"configuration repaired"}`, map[string]string{"processID": "process-1"})
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
