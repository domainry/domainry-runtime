package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

const (
	routeWorkflowKey  = "expense.route_approval"
	routeSubmitKey    = "expense_request.submit"
	routeApproverRole = "route_approver"
	routeAdminRole    = "route_admin"
	routeInitiator    = "route_initiator"
	routeTypeBase     = "example.com/domainry/integrationtest/route."
)

// routeSubmitInput is the typed shape a generated Handler decodes. The route
// arrives as ordinary structured Action input; Runtime owns every rule that
// turns it into a durable approval route.
type routeSubmitInput struct {
	Title string `json:"title"`
	Steps []struct {
		StepKey           string   `json:"step_key"`
		Title             string   `json:"title"`
		Mode              string   `json:"mode"`
		RequiredApprovals int      `json:"required_approvals"`
		Assignees         []string `json:"assignees"`
		Deferred          bool     `json:"deferred"`
	} `json:"steps"`
}

type routeSubmitHandler struct{ descriptor runtimeext.HandlerDescriptor }

func (h *routeSubmitHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *routeSubmitHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	var input routeSubmitInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, &runtimeext.BusinessError{Code: "route.decode_failed", Message: err.Error()}
	}
	created, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: "expense_request", Fields: map[string]any{"title": input.Title, "state": "submitted"},
	})
	if err != nil {
		return nil, err
	}
	start := runtimeext.WorkflowStart{WorkflowKey: routeWorkflowKey, ObjectKey: "expense_request", RecordID: created.Record.ID, Variables: map[string]any{"title": input.Title}}
	for _, step := range input.Steps {
		start.Route = append(start.Route, runtimeext.WorkflowRouteStep{
			StepKey: step.StepKey, Title: step.Title, Mode: step.Mode, RequiredApprovals: step.RequiredApprovals,
			AssigneeUserIDs: step.Assignees, Deferred: step.Deferred,
		})
	}
	receipt, err := runtimeext.StageWorkflowStart(ctx, execution, start)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"record_id": created.Record.ID, "process_id": receipt.ProcessID, "step_keys": receipt.StepKeys})
}

func routeWorkflowManifest(t *testing.T, directory string, route map[string]any) string {
	t.Helper()
	submit := map[string]any{
		"key": routeSubmitKey, "object_key": "expense_request", "label": "Submit expense", "kind": "object_operation",
		"preconditions": []any{}, "audit_event": "expense_submitted",
		"input_type": routeTypeBase + "SubmitInput", "output_type": routeTypeBase + "SubmitOutput",
		"input_contract_sha256":  sourceOwnedFixtureContractHash(routeSubmitKey + ":input"),
		"output_contract_sha256": sourceOwnedFixtureContractHash(routeSubmitKey + ":output"),
		"payload_fields": []any{
			map[string]any{"key": "title", "name": "Title", "type": "text", "required": true},
			map[string]any{"key": "steps", "name": "Steps", "type": "object", "repeated": true, "max_items": 5, "fields": []any{
				map[string]any{"key": "step_key", "type": "text"},
				map[string]any{"key": "title", "type": "text"},
				map[string]any{"key": "mode", "type": "text"},
				map[string]any{"key": "required_approvals", "type": "integer"},
				map[string]any{"key": "assignees", "type": "user", "repeated": true},
				map[string]any{"key": "deferred", "type": "boolean"},
			}},
		},
	}
	trigger := map[string]any{"type": "manual"}
	workflow := map[string]any{
		"key": routeWorkflowKey, "name": "Expense route approval", "enabled": true,
		"trigger": trigger, "trigger_contract": trigger, "action": map[string]any{"type": "workflow_graph"},
		"graph": map[string]any{"version": 2,
			"nodes": []any{
				map[string]any{"id": "submitted", "type": "trigger", "name": "Submitted"},
				map[string]any{"id": "review", "type": "approval", "name": "Route review", "contract": map[string]any{
					"approval": map[string]any{"mode": "any", "empty_assignee_policy": "fail", "route": route},
				}},
			},
			"edges": []any{map[string]any{"id": "start", "source": "submitted", "target": "review"}},
		},
	}
	permissions := []any{}
	for _, permission := range routeWorkflowPermissions() {
		permissions = append(permissions, map[string]any{"permission_key": permission, "data_scope": "all"})
	}
	manifest := map[string]any{
		"template_id": "workflow_instance_route", "version": "0.1.0", "name": "Workflow Instance Route", "schema_version": "2",
		"objects": []any{map[string]any{"key": "expense_request", "name": "Expense request", "fields": []any{
			map[string]any{"key": "title", "name": "Title", "type": "text", "required": true},
			map[string]any{"key": "state", "name": "State", "type": "text"},
		}}},
		"actions":   []any{submit},
		"workflows": []any{workflow},
		"roles": []any{
			map[string]any{"key": routeApproverRole, "name": "Route approver", "permissions": permissions},
			map[string]any{"key": routeAdminRole, "name": "Route admin", "permissions": permissions},
		},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "workflow-route-manifest.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func routeWorkflowPermissions() []string {
	return append([]string{"expense_request.read", "expense_request.create", "expense_request.update", routeSubmitKey}, integrationWorkflowTaskDecisionPermissions()...)
}

type routeWorkflowUser struct {
	id, role string
	status   string
}

func newRouteWorkflowRuntime(t *testing.T, cfg config.Config, route map[string]any, users []routeWorkflowUser) *bootstrap.Runtime {
	t.Helper()
	cfg.RuntimeAllowDevIdentityHeaders = true
	cfg = initializedIntegrationRuntimeConfig(cfg)
	if strings.TrimSpace(cfg.RuntimeVersion) == "" {
		cfg.RuntimeVersion = "integrationtest-runtime-v1"
	}
	if strings.TrimSpace(cfg.ManifestPath) == "" {
		cfg.ManifestPath = routeWorkflowManifest(t, filepath.Dir(cfg.DBPath), route)
	}
	permissions := append([]string{"business.access"}, routeWorkflowPermissions()...)
	fixtureUsers := []identitysdk.User{}
	assignments := map[string][]string{}
	for _, user := range users {
		status := user.status
		if status == "" {
			status = "active"
		}
		fixtureUsers = append(fixtureUsers, identitysdk.User{ID: user.id, Name: user.id, Email: user.id + "@example.com", Status: status})
		assignments[user.id] = []string{user.role}
	}
	factory := runtimetestkit.NewIdentityFactory(runtimetestkit.IdentityFixtureConfig{
		Roles: []runtimetestkit.IdentityFixtureRole{
			integrationIdentityRole(routeApproverRole, "Route approver", permissions, true),
			integrationIdentityRole(routeAdminRole, "Route admin", permissions, true),
		},
		Users: fixtureUsers, UserRoleAssignments: assignments,
	})
	binding, err := factory.Open(t.Context(), identitysdk.ApplicationRef{
		TenantID: identitysdk.TenantID(cfg.IdentityWorkspaceID), WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey("domainry-runtime"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	registry := runtimeext.NewBusinessHandlerRegistry()
	descriptor := runtimeext.HandlerDescriptor{
		ActionKey: routeSubmitKey, InputType: routeTypeBase + "SubmitInput", OutputType: routeTypeBase + "SubmitOutput",
		InputContractSHA256: sourceOwnedFixtureContractHash(routeSubmitKey + ":input"), OutputContractSHA256: sourceOwnedFixtureContractHash(routeSubmitKey + ":output"),
		HandlerRevision:    "workflow-route-fixture-v1",
		ObjectCapabilities: []runtimeext.ActionObjectCapability{{ObjectKey: "expense_request", Operations: []string{"create", "update"}}},
		Workflows:          []runtimeext.WorkflowGrant{{Key: routeWorkflowKey, Operations: []string{runtimeext.WorkflowStartOperation}}},
	}
	if err := registry.Register(&routeSubmitHandler{descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	runtime := bootstrap.NewWithBusinessHandlersAndScheduler(t.Context(), cfg, registry, binding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), integrationAgentFactory())
	t.Cleanup(func() { _ = runtime.CloseContext(context.Background()) })
	return runtime
}

type routeResponse struct {
	status int
	body   map[string]any
	list   []any
	raw    string
}

func routeRequest(t *testing.T, handler http.Handler, userID, roleKey, method, path string, body any, headers map[string]string) routeResponse {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+runtimetestkit.IdentityFixtureAccessToken(userID, roleKey))
	request.Header.Set("X-Workspace-ID", "workspace-primary")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Idempotency-Key", method+":"+path+":"+userID+":"+time.Now().UTC().Format(time.RFC3339Nano))
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := routeResponse{status: recorder.Code, raw: recorder.Body.String()}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response.body)
	_ = json.Unmarshal(recorder.Body.Bytes(), &response.list)
	return response
}

// routeSubmit posts the submit Action and drains the committed intent so the
// staged process reaches its first approval step exactly as the runtime worker
// would after a restart.
func routeSubmit(t *testing.T, handler http.Handler, payload map[string]any, headers map[string]string) routeResponse {
	t.Helper()
	return routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPost, "/records/expense_request/actions/"+routeSubmitKey, map[string]any{"data": payload}, headers)
}

func routeOpenTasks(t *testing.T, handler http.Handler, userID string) []any {
	t.Helper()
	response := routeRequest(t, handler, userID, routeApproverRole, http.MethodGet, "/workflow/tasks?status=open&limit=20", nil, nil)
	if response.status != http.StatusOK {
		t.Fatalf("list tasks status=%d body=%s", response.status, response.raw)
	}
	return response.list
}

func routeAwaitOpenTask(t *testing.T, handler http.Handler, userID string) map[string]any {
	t.Helper()
	for attempt := 0; attempt < 40; attempt++ {
		tasks := routeOpenTasks(t, handler, userID)
		if len(tasks) == 1 {
			task, _ := tasks[0].(map[string]any)
			return task
		}
		if len(tasks) > 1 {
			t.Fatalf("user %s holds %d open tasks", userID, len(tasks))
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("user %s never received an open task", userID)
	return nil
}

func routeDefaultContract() map[string]any {
	return map[string]any{
		"source": "instance", "min_steps": 1, "max_steps": 3, "max_assignees_per_step": 3,
		"eligible_roles": []any{routeApproverRole}, "deferred_steps": "allow",
	}
}

func routeStandardUsers() []routeWorkflowUser {
	return []routeWorkflowUser{
		{id: routeInitiator, role: routeApproverRole},
		{id: "route_user_a", role: routeApproverRole},
		{id: "route_user_b", role: routeApproverRole},
		{id: "route_user_c", role: routeApproverRole},
		{id: "route_outsider", role: routeAdminRole},
	}
}

func TestWorkflowInstanceRouteStartsIndependentProcessesPerSubmission(t *testing.T) {
	temp := t.TempDir()
	runtime := newRouteWorkflowRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"),
		ManifestPath: routeWorkflowManifest(t, temp, routeDefaultContract()), UploadDir: filepath.Join(temp, "uploads"),
	}, routeDefaultContract(), routeStandardUsers())
	handler := runtime.Routes()

	twoStep := routeSubmit(t, handler, map[string]any{"title": "Laptop", "steps": []any{
		map[string]any{"step_key": "manager", "title": "Manager", "assignees": []any{"route_user_a"}},
		map[string]any{"step_key": "finance", "title": "Finance", "assignees": []any{"route_user_b"}},
	}}, nil)
	if twoStep.status != http.StatusOK {
		t.Fatalf("two-step submit status=%d body=%s", twoStep.status, twoStep.raw)
	}
	threeStep := routeSubmit(t, handler, map[string]any{"title": "Conference", "steps": []any{
		map[string]any{"step_key": "manager", "assignees": []any{"route_user_b"}},
		map[string]any{"step_key": "finance", "assignees": []any{"route_user_c"}},
		map[string]any{"step_key": "director", "assignees": []any{"route_user_a"}},
	}}, nil)
	if threeStep.status != http.StatusOK {
		t.Fatalf("three-step submit status=%d body=%s", threeStep.status, threeStep.raw)
	}
	firstOutput, _ := twoStep.body["output"].(map[string]any)
	secondOutput, _ := threeStep.body["output"].(map[string]any)
	firstProcess, _ := firstOutput["process_id"].(string)
	secondProcess, _ := secondOutput["process_id"].(string)
	if firstProcess == "" || secondProcess == "" || firstProcess == secondProcess {
		t.Fatalf("staged process identities first=%q second=%q", firstProcess, secondProcess)
	}

	// Editing the submitted record afterwards must not touch the frozen route.
	recordID, _ := firstOutput["record_id"].(string)
	patched := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPatch, "/records/expense_request/items/"+recordID, map[string]any{"data": map[string]any{"title": "Laptop (edited)", "state": "edited"}}, nil)
	if patched.status != http.StatusOK {
		t.Fatalf("record edit status=%d body=%s", patched.status, patched.raw)
	}

	firstStep := routeAwaitOpenTask(t, handler, "route_user_a")
	if firstStep["process_id"] != firstProcess {
		t.Fatalf("first step task=%#v want process %s", firstStep, firstProcess)
	}
	route := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+firstProcess+"/route", nil, nil)
	if route.status != http.StatusOK {
		t.Fatalf("route status=%d body=%s", route.status, route.raw)
	}
	steps, _ := route.body["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("route steps=%s", route.raw)
	}
	first, _ := steps[0].(map[string]any)
	if first["status"] != "active" || first["step_key"] != "manager" || first["title"] != "Manager" {
		t.Fatalf("first route step=%#v", first)
	}
	second, _ := steps[1].(map[string]any)
	if second["status"] != "pending" || second["assignee_count"] != float64(1) {
		t.Fatalf("second route step=%#v", second)
	}
}

func TestWorkflowInstanceRouteRejectsInvalidRoutesWithFieldPaths(t *testing.T) {
	temp := t.TempDir()
	users := append(routeStandardUsers(), routeWorkflowUser{id: "route_inactive", role: routeApproverRole, status: "disabled"})
	contract := routeDefaultContract()
	contract["max_assignees_per_step"] = 2
	runtime := newRouteWorkflowRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"),
		ManifestPath: routeWorkflowManifest(t, temp, contract), UploadDir: filepath.Join(temp, "uploads"),
	}, contract, users)
	handler := runtime.Routes()
	for _, test := range []struct {
		name      string
		steps     []any
		code      string
		fieldPath string
	}{
		{"too many steps", []any{
			map[string]any{"assignees": []any{"route_user_a"}}, map[string]any{"assignees": []any{"route_user_b"}},
			map[string]any{"assignees": []any{"route_user_c"}}, map[string]any{"assignees": []any{"route_user_a"}},
		}, "backend.workflow.route_step_count_invalid", "route.steps"},
		{"unknown user", []any{map[string]any{"assignees": []any{"route_ghost"}}}, "backend.workflow.route_assignee_not_found", "route.steps[0].assignees[0]"},
		{"inactive user", []any{map[string]any{"assignees": []any{"route_inactive"}}}, "backend.workflow.route_assignee_inactive", "route.steps[0].assignees[0]"},
		{"ineligible role", []any{map[string]any{"assignees": []any{"route_outsider"}}}, "backend.workflow.route_assignee_not_eligible", "route.steps[0].assignees[0]"},
		{"duplicate user", []any{map[string]any{"assignees": []any{"route_user_a", "route_user_a"}}}, "backend.workflow.route_assignee_duplicate", "route.steps[0].assignees[1]"},
		{"quorum out of range", []any{map[string]any{"mode": "quorum", "required_approvals": 3, "assignees": []any{"route_user_a", "route_user_b"}}}, "backend.workflow.route_required_approvals_invalid", "route.steps[0].required_approvals"},
		{"deferred first step", []any{map[string]any{"deferred": true}}, "backend.workflow.route_deferred_not_allowed", "route.steps[0].deferred"},
		{"empty step", []any{map[string]any{"assignees": []any{}}}, "backend.workflow.route_step_count_invalid", "route.steps[0].assignees"},
		{"duplicate step key", []any{
			map[string]any{"step_key": "same", "assignees": []any{"route_user_a"}},
			map[string]any{"step_key": "same", "assignees": []any{"route_user_b"}},
		}, "backend.workflow.route_step_key_duplicate", "route.steps[1].step_key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := routeSubmit(t, handler, map[string]any{"title": test.name, "steps": test.steps}, nil)
			if response.status != http.StatusBadRequest || response.body["code"] != test.code {
				t.Fatalf("status=%d body=%s", response.status, response.raw)
			}
			params, _ := response.body["params"].(map[string]any)
			if params["field_path"] != test.fieldPath {
				t.Fatalf("field_path=%v want=%s body=%s", params["field_path"], test.fieldPath, response.raw)
			}
		})
	}
	if listed := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes?limit=50", nil, nil); len(listed.list) != 0 {
		t.Fatalf("a rejected route left a process behind: %s", listed.raw)
	}
}

func TestWorkflowInstanceRouteLeavesNothingBehindWhenTheActionAborts(t *testing.T) {
	temp := t.TempDir()
	runtime := newRouteWorkflowRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"),
		ManifestPath: routeWorkflowManifest(t, temp, routeDefaultContract()), UploadDir: filepath.Join(temp, "uploads"),
	}, routeDefaultContract(), routeStandardUsers())
	handler := runtime.Routes()
	payload := map[string]any{"title": "Aborted", "steps": []any{map[string]any{"assignees": []any{"route_user_a"}}}}
	aborted := routeSubmit(t, handler, payload, map[string]string{actionmodel.AcceptanceFailureHeader: actionmodel.AcceptanceFailureBeforeCommit})
	if aborted.status != http.StatusInternalServerError || aborted.body["code"] != actionmodel.AcceptanceFailureInjectedCode {
		t.Fatalf("injected failure status=%d body=%s", aborted.status, aborted.raw)
	}
	records := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/records/expense_request?page=1&page_size=50", nil, nil)
	if strings.Contains(records.raw, "Aborted") {
		t.Fatalf("the aborted application record survived: %s", records.raw)
	}
	processes := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes?limit=50", nil, nil)
	if len(processes.list) != 0 {
		t.Fatalf("the aborted Action left a process behind: %s", processes.raw)
	}
	if tasks := routeOpenTasks(t, handler, "route_user_a"); len(tasks) != 0 {
		t.Fatalf("the aborted Action left tasks behind: %#v", tasks)
	}
	committed := routeSubmit(t, handler, payload, nil)
	if committed.status != http.StatusOK {
		t.Fatalf("submit without injection status=%d body=%s", committed.status, committed.raw)
	}
	routeAwaitOpenTask(t, handler, "route_user_a")
}
