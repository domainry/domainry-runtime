package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeAPIContractIdentityIsStable(t *testing.T) {
	if RuntimeAPIContractVersion != "runtime-domain-api-v1" {
		t.Fatalf("unexpected Runtime API contract version %q", RuntimeAPIContractVersion)
	}
	if hash := RuntimeAPIContractHash(); len(hash) != 64 || hash != RuntimeAPIContractHash() {
		t.Fatalf("Runtime API contract hash is not stable: %q", hash)
	}
	var document struct {
		ContractHash string `json:"contract_hash"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	if document.ContractHash != RuntimeAPIContractHash() {
		t.Fatalf("published Runtime API hash=%q, generated identity=%q", document.ContractHash, RuntimeAPIContractHash())
	}
}

func TestRuntimeAPIContractPublishesActionInvocationScope(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"routes"`
		Schemas map[string]struct {
			InvocationScopeByKind map[string][]string `json:"invocation_scope_by_kind"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	if route := document.Routes["action_catalog"]; route.Method != "GET" || route.Path != "/objects/{objectKey}/actions" {
		t.Fatalf("unexpected Action catalog route: %+v", route)
	}
	scope := document.Schemas["action_definition"].InvocationScopeByKind
	if !reflect.DeepEqual(scope["object"], []string{"object_create", "object_operation", "bulk_operation"}) {
		t.Fatalf("unexpected object Action kinds: %v", scope["object"])
	}
	if !reflect.DeepEqual(scope["record"], []string{"record_update", "record_delete", "record_restore", "transition_state", "conditional_update", "record_operation"}) {
		t.Fatalf("unexpected record Action kinds: %v", scope["record"])
	}
}

func TestRuntimeAPIContractPublishesFoundationReads(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method   string            `json:"method"`
			Path     string            `json:"path"`
			Response string            `json:"response"`
			Fixed    map[string]string `json:"fixed_query"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
			Roles    string   `json:"roles"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		path     string
		response string
	}{
		"job_catalog_list": {"/foundation/jobs", "job_catalog_page"},
		"position_list":    {"/foundation/positions", "position_page"},
	}
	for key, want := range expected {
		route, ok := document.Routes[key]
		if !ok || route.Method != "GET" || route.Path != want.path || route.Response != want.response {
			t.Fatalf("unexpected %s route: %+v", key, route)
		}
	}
}

func TestRuntimeAPIContractPublishesBusinessAgentRoutesWithoutOperationsControls(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method, Path, Request, Response string
			RequiresIdempotencyKey          bool `json:"requires_idempotency_key"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
			Optional []string `json:"optional"`
			Items    string   `json:"items"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct{ method, path string }{
		"agent_interactive_run":          {"POST", "/agent-dialog/runs"},
		"agent_interactive_run_stream":   {"POST", "/agent-dialog/runs/stream"},
		"agent_analysis_query":           {"POST", "/agent-dialog/analysis/query"},
		"agent_interactive_run_get":      {"GET", "/agent-dialog/runs/{runID}"},
		"agent_task_run_get":             {"GET", "/agent-dialog/task-runs/{taskRunID}"},
		"agent_session_list":             {"GET", "/agent-dialog/sessions"},
		"agent_session_upsert":           {"POST", "/agent-dialog/sessions"},
		"agent_proposal_list":            {"GET", "/agent-dialog/proposals"},
		"agent_proposal_create":          {"POST", "/agent-dialog/proposals"},
		"agent_proposal_approve":         {"POST", "/agent-dialog/proposals/{proposalID}/approve"},
		"agent_proposal_reject":          {"POST", "/agent-dialog/proposals/{proposalID}/reject"},
		"agent_report_query_run_get":     {"GET", "/agent-dialog/report-query-runs/{queryRef}"},
		"agent_report_export_audit_get":  {"GET", "/agent-dialog/report-export-audits/{queryRef}"},
		"agent_report_download_task_get": {"GET", "/agent-dialog/download-tasks/{queryRef}"},
		"agent_report_handoff_prepare":   {"POST", "/agent-dialog/download-tasks/{queryRef}/prepare"},
	}
	for key, want := range expected {
		route, ok := document.Routes[key]
		if !ok || route.Method != want.method || route.Path != want.path {
			t.Errorf("Agent route %s=%+v", key, route)
		}
	}
	if !document.Routes["agent_interactive_run"].RequiresIdempotencyKey || document.Routes["agent_interactive_run"].Request != "agent_interactive_run_request" {
		t.Fatalf("interactive Agent route lacks idempotency/request contract: %+v", document.Routes["agent_interactive_run"])
	}
	for key, route := range document.Routes {
		if strings.HasPrefix(route.Path, "/operations/agent/") || route.Path == "/agent-dialog/diagnostics" {
			t.Errorf("operations-only Agent route leaked into Business Runtime API contract as %s", key)
		}
	}
	for key := range map[string]bool{"agent_interactive_run": true, "agent_task_run": true, "agent_session": true, "agent_proposal": true} {
		if len(document.Schemas[key].Required) == 0 {
			t.Errorf("Agent schema %s has no stable required projection", key)
		}
	}
	if document.Schemas["agent_session_list"].Items != "agent_session" || document.Schemas["agent_proposal_list"].Items != "agent_proposal" {
		t.Fatalf("Agent list schema drift: sessions=%+v proposals=%+v", document.Schemas["agent_session_list"], document.Schemas["agent_proposal_list"])
	}
}

func TestRuntimeAPIContractRouteSchemasAreClosed(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Request, Response string
		} `json:"routes"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for name, route := range document.Routes {
		for kind, schema := range map[string]string{"request": route.Request, "response": route.Response} {
			if schema == "" || schema == "empty" {
				continue
			}
			if _, ok := document.Schemas[schema]; !ok {
				t.Errorf("route %s %s references missing schema %s", name, kind, schema)
			}
		}
	}
}

func TestRuntimeAPIContractPublishesGovernedReportDataExchangeJob(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Request, Response string
		} `json:"routes"`
		Schemas map[string]struct {
			Required    []string            `json:"required"`
			Optional    []string            `json:"optional"`
			FieldValues map[string][]string `json:"field_values"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	if route := document.Routes["report_export_prepare"]; route.Request != "report_export_prepare_request" || route.Response != "report_export_job" {
		t.Fatalf("route=%+v", route)
	}
	if document.Routes["report_export_job_get"].Response != "report_export_job" || document.Routes["report_export_job_cancel"].Response != "report_export_job" {
		t.Fatalf("report export lifecycle routes=%+v/%+v", document.Routes["report_export_job_get"], document.Routes["report_export_job_cancel"])
	}
	contains := func(values []string, key string) bool {
		for _, value := range values {
			if value == key {
				return true
			}
		}
		return false
	}
	if !contains(document.Schemas["report_export_prepare_request"].Required, "scope") || contains(document.Schemas["report_export_prepare_request"].Optional, "scope") {
		t.Fatalf("prepare schema=%+v", document.Schemas["report_export_prepare_request"])
	}
	for _, key := range []string{"page_size", "truncated", "total", "total_semantics"} {
		if !contains(document.Schemas["report_summary"].Required, key) {
			t.Fatalf("report summary missing pagination field %s: %+v", key, document.Schemas["report_summary"])
		}
	}
	if !reflect.DeepEqual(document.Schemas["report_summary"].FieldValues["total_semantics"], []string{"exact", "at_least"}) {
		t.Fatalf("report summary total semantics=%v", document.Schemas["report_summary"].FieldValues["total_semantics"])
	}
	if !contains(document.Schemas["report_export_job"].Required, "data_exchange_job_id") || contains(document.Schemas["report_export_job"].Required, "batch_job_id") {
		t.Fatalf("report export job schema=%+v", document.Schemas["report_export_job"])
	}
	for _, key := range []string{"purpose", "freshness", "role_key", "data_scopes", "metric_definitions"} {
		if !contains(document.Schemas["report_export_scope"].Required, key) {
			t.Fatalf("scope missing %s: %+v", key, document.Schemas["report_export_scope"])
		}
	}
	if !contains(document.Schemas["report_export_scope"].Optional, "tag_match") {
		t.Fatalf("scope missing tag_match: %+v", document.Schemas["report_export_scope"])
	}
	for _, key := range []string{"content_sha256", "row_count", "scope"} {
		if !contains(document.Schemas["report_export_download"].Required, key) {
			t.Fatalf("download missing %s: %+v", key, document.Schemas["report_export_download"])
		}
	}
}

func TestRuntimeAPIContractPublishesObjectSQLQueryAndExportParameters(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method, Path, Request, Response string
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
			Optional []string `json:"optional"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	route := document.Routes["report_object_sql_query"]
	if route.Method != "POST" || route.Path != "/reports/{reportKey}/query" || route.Request != "report_object_sql_query_request" || route.Response != "report_summary" {
		t.Fatalf("object SQL route=%+v", route)
	}
	if !reflect.DeepEqual(document.Schemas["report_object_sql_query_request"].Required, []string{"parameters"}) {
		t.Fatalf("object SQL request schema=%+v", document.Schemas["report_object_sql_query_request"])
	}
	if !stringSliceContains(document.Schemas["report_summary"].Optional, "result_schema") || !stringSliceContains(document.Schemas["report_export_scope"].Optional, "parameters") {
		t.Fatalf("report schemas=%+v %+v", document.Schemas["report_summary"], document.Schemas["report_export_scope"])
	}
}

func stringSliceContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestRuntimeAPIContractPublishesFoundationAuthoring(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method   string `json:"method"`
			Path     string `json:"path"`
			Request  string `json:"request"`
			Response string `json:"response"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]struct {
		method string
		path   string
	}{
		"job_catalog_get":    {"GET", "/foundation/jobs/{jobID}"},
		"job_catalog_upsert": {"PUT", "/foundation/jobs/{jobID}"},
		"position_get":       {"GET", "/foundation/positions/{positionID}"},
		"position_upsert":    {"PUT", "/foundation/positions/{positionID}"},
	} {
		route, ok := document.Routes[key]
		if !ok || route.Method != expected.method || route.Path != expected.path {
			t.Fatalf("unexpected %s route: %+v", key, route)
		}
	}
}

func TestRuntimeAPIContractPublishesBusinessWorkflowLifecycle(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method                 string   `json:"method"`
			Path                   string   `json:"path"`
			Response               string   `json:"response"`
			RequiresIdempotencyKey bool     `json:"requires_idempotency_key"`
			Query                  []string `json:"query"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]struct {
		method   string
		path     string
		response string
		mutation bool
	}{
		"workflow_process_list":     {"GET", "/business/workflow/processes", "workflow_process_list", false},
		"workflow_process_detail":   {"GET", "/business/workflow/processes/{processID}", "workflow_process_detail", false},
		"workflow_task_list":        {"GET", "/business/workflow/tasks", "workflow_task_list", false},
		"workflow_team_task_list":   {"GET", "/business/workflow/team-tasks", "workflow_task_list", false},
		"workflow_run":              {"POST", "/business/workflows/{workflowKey}/run", "workflow_run_result", true},
		"portal_workflow_run":       {"POST", "/portal/workflows/{workflowKey}/run", "workflow_run_result", true},
		"workflow_task_approve":     {"POST", "/business/workflow/tasks/{taskID}/approve", "workflow_process", true},
		"workflow_task_reject":      {"POST", "/business/workflow/tasks/{taskID}/reject", "workflow_process", true},
		"workflow_task_return":      {"POST", "/business/workflow/tasks/{taskID}/return", "workflow_process", true},
		"workflow_process_withdraw": {"POST", "/business/workflow/processes/{processID}/withdraw", "workflow_process", true},
		"workflow_process_retry":    {"POST", "/business/workflow/processes/{processID}/retry", "workflow_process", true},
	} {
		route, ok := document.Routes[key]
		if !ok || route.Method != expected.method || route.Path != expected.path || route.Response != expected.response ||
			route.RequiresIdempotencyKey != expected.mutation {
			t.Fatalf("unexpected %s route: %+v", key, route)
		}
	}
	required := document.Schemas["workflow_process_detail"].Required
	if !reflect.DeepEqual(required, []string{"process", "nodes", "tasks", "events"}) {
		t.Fatalf("unexpected workflow detail schema: %v", required)
	}
}

func TestRuntimeAPIContractPublishesBusinessAuditQuery(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method   string   `json:"method"`
			Path     string   `json:"path"`
			Response string   `json:"response"`
			Query    []string `json:"query"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
			Items    string   `json:"items"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	route := document.Routes["business_audit_event_list"]
	if route.Method != "GET" || route.Path != "/business/audit-events" ||
		route.Response != "business_audit_event_page" || len(route.Query) != 9 {
		t.Fatalf("unexpected business audit route: %+v", route)
	}
	if document.Schemas["business_audit_event_page"].Items != "business_audit_event" ||
		!reflect.DeepEqual(document.Schemas["business_audit_event_page"].Required,
			[]string{"items", "count", "retention_class", "retention_days"}) {
		t.Fatalf("unexpected business audit page schema: %+v", document.Schemas["business_audit_event_page"])
	}
	if !reflect.DeepEqual(document.Schemas["business_audit_event"].Required,
		[]string{"id", "workspace_id", "event", "actor_id", "role_key", "summary", "created_at"}) {
		t.Fatalf("unexpected business audit schema: %+v", document.Schemas["business_audit_event"])
	}
}

func TestRuntimeAPIContractPublishesGovernedBusinessAuditExport(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Method                 string `json:"method"`
			Path                   string `json:"path"`
			Request                string `json:"request"`
			Response               string `json:"response"`
			RequiresIdempotencyKey bool   `json:"requires_idempotency_key"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
			Optional []string `json:"optional"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	prepare := document.Routes["business_audit_event_export_prepare"]
	if prepare.Method != "POST" || prepare.Path != "/business/audit-event-exports" || prepare.Request != "business_audit_event_export_request" || prepare.Response != "business_audit_event_export_prepared" || !prepare.RequiresIdempotencyKey {
		t.Fatalf("unexpected prepare route: %+v", prepare)
	}
	download := document.Routes["business_audit_event_export_download"]
	if download.Method != "GET" || download.Path != "/business/audit-event-exports/downloads/{token}" || download.Response != "file" {
		t.Fatalf("unexpected download route: %+v", download)
	}
	if !reflect.DeepEqual(document.Schemas["business_audit_event_export_request"].Required, []string{"filters"}) {
		t.Fatalf("request schema=%+v", document.Schemas["business_audit_event_export_request"])
	}
	if !reflect.DeepEqual(document.Schemas["business_audit_event_export_filter"].Optional, []string{"event", "object_key", "record_id", "actor_id", "role_key", "result", "created_from", "created_to"}) {
		t.Fatalf("filter schema=%+v", document.Schemas["business_audit_event_export_filter"])
	}
	for _, key := range []string{"content_sha256", "row_count", "audit_identity", "scope_sha256", "download_token", "expires_at"} {
		found := false
		for _, required := range document.Schemas["business_audit_event_export_prepared"].Required {
			found = found || required == key
		}
		if !found {
			t.Fatalf("prepared schema missing %s: %+v", key, document.Schemas["business_audit_event_export_prepared"])
		}
	}
}
