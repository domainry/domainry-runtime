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

func TestRuntimeAPIContractPublishesClientOwnedIdempotencyLifecycle(t *testing.T) {
	var document struct {
		Identity struct {
			Idempotency map[string]string `json:"idempotency"`
		} `json:"identity"`
		Schemas map[string]struct {
			Optional []string `json:"optional"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	policy := document.Identity.Idempotency
	if policy["request_header"] != "Idempotency-Key" || policy["key_source"] != "client_logical_operation_id" || policy["retry_policy"] != "reuse_same_key" || policy["new_operation_policy"] != "new_key" {
		t.Fatalf("idempotency lifecycle=%v", policy)
	}
	if stringSliceContains(document.Schemas["record_bulk_action_request"].Optional, "idempotency_key") {
		t.Fatal("bulk Action request still exposes transport idempotency in its body")
	}
}

func TestRuntimeAPIContractDoesNotUseFrontendSurfaceForEndpointBehavior(t *testing.T) {
	var document struct {
		Identity map[string]json.RawMessage `json:"identity"`
		Routes   map[string]struct {
			Query []string `json:"query"`
		} `json:"routes"`
		Schemas map[string]struct {
			Required []string `json:"required"`
		} `json:"schemas"`
		AdminBusinessReuse json.RawMessage `json:"admin_business_reuse"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document.Identity["surface"]; exists || len(document.AdminBusinessReuse) != 0 {
		t.Fatalf("frontend Surface contract leaked into Runtime API: identity=%v admin_business_reuse=%s", document.Identity, document.AdminBusinessReuse)
	}
	if stringSliceContains(document.Routes["agent_session_list"].Query, "surface") ||
		stringSliceContains(document.Schemas["agent_interactive_run"].Required, "surface") {
		t.Fatal("Agent API contract still partitions requests or runs by frontend Surface")
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

func TestRuntimeAPIContractDoesNotRepublishPartyFoundation(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Path string `json:"path"`
		} `json:"routes"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for key, route := range document.Routes {
		if strings.HasPrefix(route.Path, "/foundation/jobs") || strings.HasPrefix(route.Path, "/foundation/positions") {
			t.Errorf("Runtime contract retained Party-owned route %s=%s", key, route.Path)
		}
	}
	for _, key := range []string{"job_catalog_item", "job_catalog_page", "position", "position_page"} {
		if _, exists := document.Schemas[key]; exists {
			t.Errorf("Runtime contract retained Party-owned schema %s", key)
		}
	}
}

func TestRuntimeAPIContractDoesNotRepublishAgentOwnedHTTP(t *testing.T) {
	var document struct {
		Routes map[string]struct {
			Path string `json:"path"`
		} `json:"routes"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for key, route := range document.Routes {
		if strings.HasPrefix(key, "agent_") || strings.HasPrefix(route.Path, "/agent-dialog/") || strings.HasPrefix(route.Path, "/operations/agent/") {
			t.Errorf("Runtime contract retained Agent-owned route %s=%s", key, route.Path)
		}
	}
	for key := range document.Schemas {
		if strings.HasPrefix(key, "agent_") {
			t.Errorf("Runtime contract retained Agent-owned schema %s", key)
		}
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

func TestRuntimeAPIContractDoesNotRepublishReportOwnedSurface(t *testing.T) {
	var document struct {
		Routes  map[string]json.RawMessage `json:"routes"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"report_summary", "report_object_sql_query", "report_snapshot_refresh", "report_export_prepare", "report_export_job_get", "report_export_job_cancel", "report_export_download"} {
		if _, exists := document.Routes[key]; exists {
			t.Fatalf("Report-owned route %q remains in Runtime static contract", key)
		}
	}
	for _, key := range []string{"report_summary", "report_object_sql_query_request", "report_snapshot", "report_export_prepare_request", "report_export_job", "report_export_scope"} {
		if _, exists := document.Schemas[key]; exists {
			t.Fatalf("Report-owned schema %q remains in Runtime static contract", key)
		}
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

func TestRuntimeAPIContractKeepsOnlyRuntimePublicationHandoff(t *testing.T) {
	var document struct {
		AdminBusinessReuse struct {
			Routes []struct {
				EndpointIdentity string `json:"endpoint_identity"`
				SourceOwner      string `json:"source_owner"`
			} `json:"routes"`
		} `json:"admin_business_reuse"`
		Routes map[string]struct {
			Path string `json:"path"`
		} `json:"routes"`
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(RuntimeAPIContractDocument(), &document); err != nil {
		t.Fatal(err)
	}
	if route := document.Routes["publication_handoff_result"]; route.Path != "/business/publication-handoffs/{messageID}" {
		t.Fatalf("publication handoff route=%+v", route)
	}
	for key, route := range document.Routes {
		if strings.Contains(key, "web_push") || strings.Contains(route.Path, "/integration-intents") || strings.Contains(route.Path, "/notifications/web-push") {
			t.Errorf("Integration-owned route leaked into Runtime API contract: %s=%s", key, route.Path)
		}
	}
	for key := range document.Schemas {
		if strings.Contains(key, "web_push") || key == "integration_intent_result" {
			t.Errorf("Integration-owned schema leaked into Runtime API contract: %s", key)
		}
	}
	for _, route := range document.AdminBusinessReuse.Routes {
		if route.SourceOwner == "integrations" || strings.Contains(route.EndpointIdentity, "/notifications/web-push") {
			t.Errorf("Integration-owned admin reuse leaked into Runtime API contract: %+v", route)
		}
	}
}
