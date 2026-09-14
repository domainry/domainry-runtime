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
	"sync"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
	return append([]string{
		"expense_request.read", "expense_request.create", "expense_request.update", routeSubmitKey,
		"runtime.workflows.process_ops_workflow_executions",
	}, integrationWorkflowTaskDecisionPermissions()...)
}

type routeWorkflowUser struct {
	id, role string
	status   string
}

func newRouteWorkflowRuntime(t *testing.T, cfg config.Config, route map[string]any, users []routeWorkflowUser, additionalHandlers ...runtimeext.BusinessHandler) *bootstrap.Runtime {
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
	for _, handler := range additionalHandlers {
		permissions = append(permissions, handler.Descriptor().ActionKey)
	}
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
		WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey("domainry-runtime"),
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
	for _, handler := range additionalHandlers {
		if err := registry.Register(handler); err != nil {
			t.Fatal(err)
		}
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

func routeDecide(t *testing.T, handler http.Handler, userID, taskID, decision string, body map[string]any, key string) routeResponse {
	t.Helper()
	headers := map[string]string{}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	return routeRequest(t, handler, userID, routeApproverRole, http.MethodPost, "/workflow/tasks/"+taskID+"/"+decision, body, headers)
}

func routeDeferredSubmit(t *testing.T, handler http.Handler, title string, firstStep map[string]any) routeResponse {
	t.Helper()
	steps := []any{firstStep, map[string]any{"step_key": "second", "title": "Second", "deferred": true}}
	return routeSubmit(t, handler, map[string]any{"title": title, "steps": steps}, nil)
}

func routeDeferredRuntime(t *testing.T) (http.Handler, string) {
	t.Helper()
	temp := t.TempDir()
	contract := routeDefaultContract()
	runtime := newRouteWorkflowRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"),
		ManifestPath: routeWorkflowManifest(t, temp, contract), UploadDir: filepath.Join(temp, "uploads"),
	}, contract, append(routeStandardUsers(), routeWorkflowUser{id: "route_inactive", role: routeApproverRole}))
	return runtime.Routes(), temp
}

func TestWorkflowInstanceRouteRefusesAnUnconfiguredNextStepAndKeepsTheTaskOpen(t *testing.T) {
	handler, _ := routeDeferredRuntime(t)
	submitted := routeDeferredSubmit(t, handler, "Deferred", map[string]any{"step_key": "first", "assignees": []any{"route_user_a"}})
	if submitted.status != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
	}
	output, _ := submitted.body["output"].(map[string]any)
	processID, _ := output["process_id"].(string)
	task := routeAwaitOpenTask(t, handler, "route_user_a")
	taskID, _ := task["id"].(string)

	for _, test := range []struct {
		name   string
		user   string
		body   map[string]any
		status int
		code   string
	}{
		{"missing next step", "route_user_a", map[string]any{"comment": "ok"}, http.StatusBadRequest, "backend.workflow.next_step_required"},
		{"inactive assignee", "route_user_a", map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_ghost"}}}, http.StatusBadRequest, "backend.workflow.route_assignee_not_found"},
		{"ineligible assignee", "route_user_a", map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_outsider"}}}, http.StatusBadRequest, "backend.workflow.route_assignee_not_eligible"},
		{"duplicate assignee", "route_user_a", map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_user_b", "route_user_b"}}}, http.StatusBadRequest, "backend.workflow.route_assignee_duplicate"},
		{"non configurer", "route_user_b", map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_user_c"}}}, http.StatusForbidden, "backend.workflow.task_assignee_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := routeDecide(t, handler, test.user, taskID, "approve", test.body, "")
			if response.status != test.status || response.body["code"] != test.code {
				t.Fatalf("status=%d body=%s", response.status, response.raw)
			}
		})
	}
	rejected := routeDecide(t, handler, "route_user_a", taskID, "reject", map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_user_b"}}}, "")
	if rejected.status != http.StatusBadRequest || rejected.body["code"] != "backend.workflow.next_step_not_applicable" {
		t.Fatalf("reject with next step status=%d body=%s", rejected.status, rejected.raw)
	}

	// Nothing above may have advanced the durable state.
	if len(routeOpenTasks(t, handler, "route_user_a")) != 1 {
		t.Fatal("a rejected configuration closed the open task")
	}
	route := routeRequest(t, handler, "route_user_a", routeApproverRole, http.MethodGet, "/workflow/processes/"+processID+"/route", nil, nil)
	steps, _ := route.body["steps"].([]any)
	second, _ := steps[1].(map[string]any)
	if second["status"] != "configurable" || second["assignee_count"] != float64(0) {
		t.Fatalf("route step after rejected configurations=%s", route.raw)
	}
	pending, _ := route.body["pending_configuration"].([]any)
	if len(pending) != 1 || pending[0] != "second" || route.body["configurable_by_me"] != true {
		t.Fatalf("pending configuration=%s", route.raw)
	}
	detail := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID, nil, nil)
	if !strings.Contains(detail.raw, `"route"`) || !strings.Contains(detail.raw, `"pending_configuration"`) {
		t.Fatalf("process detail lost the route: %s", detail.raw)
	}
}

func TestWorkflowInstanceRouteConfiguresTheNextStepOnceAndReplaysTheSameKey(t *testing.T) {
	handler, _ := routeDeferredRuntime(t)
	submitted := routeDeferredSubmit(t, handler, "Quorum", map[string]any{
		"step_key": "first", "mode": "quorum", "required_approvals": 2, "assignees": []any{"route_user_a", "route_user_b", "route_user_c"},
	})
	if submitted.status != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
	}
	output, _ := submitted.body["output"].(map[string]any)
	processID, _ := output["process_id"].(string)
	first := routeAwaitOpenTask(t, handler, "route_user_a")
	firstID, _ := first["id"].(string)
	// The next step must name an approver outside the current electorate so an
	// activated task cannot be confused with a step-1 task.
	configuration := map[string]any{"next_step": map[string]any{"step_key": "second", "assignee_user_ids": []any{routeInitiator}}}

	// The first of two required approvals carries the configuration and must
	// not open the next step yet.
	for range 2 {
		response := routeDecide(t, handler, "route_user_a", firstID, "approve", configuration, "first-vote")
		if response.status != http.StatusOK || response.body["status"] != "waiting" {
			t.Fatalf("first vote/replay status=%d body=%s", response.status, response.raw)
		}
	}
	if tasks := routeOpenTasks(t, handler, routeInitiator); len(tasks) != 0 {
		t.Fatalf("an unfinished quorum activated the next step: %#v", tasks)
	}
	route := routeRequest(t, handler, "route_user_a", routeApproverRole, http.MethodGet, "/workflow/processes/"+processID+"/route", nil, nil)
	steps, _ := route.body["steps"].([]any)
	second, _ := steps[1].(map[string]any)
	if second["status"] != "pending" || second["configured_by"] != "route_user_a" || second["configure_source"] != "decision" || second["assignee_count"] != float64(1) {
		t.Fatalf("configured step=%s", route.raw)
	}

	// A different configuration from the second approver is a conflict.
	conflicting := map[string]any{"next_step": map[string]any{"assignee_user_ids": []any{"route_user_a"}}}
	response := routeDecide(t, handler, "route_user_b", firstID, "approve", conflicting, "")
	if response.status != http.StatusForbidden {
		t.Fatalf("foreign task decision status=%d body=%s", response.status, response.raw)
	}
	secondTask := map[string]any{}
	for _, candidate := range routeOpenTasks(t, handler, "route_user_b") {
		secondTask, _ = candidate.(map[string]any)
	}
	secondID, _ := secondTask["id"].(string)
	response = routeDecide(t, handler, "route_user_b", secondID, "approve", conflicting, "")
	if response.status != http.StatusConflict || response.body["code"] != "backend.workflow.next_step_already_configured" {
		t.Fatalf("conflicting configuration status=%d body=%s", response.status, response.raw)
	}
	params, _ := response.body["params"].(map[string]any)
	if params["assignee_user_ids"] != routeInitiator || params["step_key"] != "second" {
		t.Fatalf("conflict params=%#v", params)
	}

	// The identical configuration is idempotent and completes the quorum.
	response = routeDecide(t, handler, "route_user_b", secondID, "approve", configuration, "second-vote")
	if response.status != http.StatusOK {
		t.Fatalf("second vote status=%d body=%s", response.status, response.raw)
	}
	activated := routeAwaitOpenTask(t, handler, routeInitiator)
	if activated["process_id"] != processID {
		t.Fatalf("activated task=%#v", activated)
	}
	if tasks := routeOpenTasks(t, handler, "route_user_a"); len(tasks) != 0 {
		t.Fatalf("the completed step left tasks open: %#v", tasks)
	}
	route = routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID+"/route", nil, nil)
	steps, _ = route.body["steps"].([]any)
	firstStep, _ := steps[0].(map[string]any)
	second, _ = steps[1].(map[string]any)
	if firstStep["status"] != "approved" || firstStep["approved_count"] != float64(2) || second["status"] != "active" {
		t.Fatalf("route after the completed step=%s", route.raw)
	}
}

func TestWorkflowInstanceRouteVisibilityIsLimitedToItsParticipants(t *testing.T) {
	handler, _ := routeDeferredRuntime(t)
	submitted := routeSubmit(t, handler, map[string]any{"title": "Visibility", "steps": []any{
		map[string]any{"step_key": "first", "assignees": []any{"route_user_a"}},
		map[string]any{"step_key": "second", "assignees": []any{"route_user_b"}},
	}}, nil)
	if submitted.status != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
	}
	output, _ := submitted.body["output"].(map[string]any)
	processID, _ := output["process_id"].(string)
	routeAwaitOpenTask(t, handler, "route_user_a")
	path := "/workflow/processes/" + processID + "/route"

	initiator := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, path, nil, nil)
	steps, _ := initiator.body["steps"].([]any)
	if initiator.status != http.StatusOK || len(steps) != 2 {
		t.Fatalf("initiator route status=%d body=%s", initiator.status, initiator.raw)
	}
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		if assignees, _ := step["assignees"].([]any); len(assignees) != 1 {
			t.Fatalf("the initiator must see every assignee: %s", initiator.raw)
		}
	}
	secondApprover := routeRequest(t, handler, "route_user_b", routeApproverRole, http.MethodGet, path, nil, nil)
	if secondApprover.status != http.StatusOK {
		t.Fatalf("step-2 assignee route status=%d body=%s", secondApprover.status, secondApprover.raw)
	}
	steps, _ = secondApprover.body["steps"].([]any)
	firstStep, _ := steps[0].(map[string]any)
	secondStep, _ := steps[1].(map[string]any)
	if assignees, _ := firstStep["assignees"].([]any); len(assignees) != 0 {
		t.Fatalf("a step-2 assignee saw the active step-1 approvers: %s", secondApprover.raw)
	}
	if assignees, _ := secondStep["assignees"].([]any); len(assignees) != 1 {
		t.Fatalf("a step-2 assignee must see its own step: %s", secondApprover.raw)
	}
	if firstStep["assignee_count"] != float64(1) {
		t.Fatalf("assignee counts must stay visible: %s", secondApprover.raw)
	}
	outsider := routeRequest(t, handler, "route_outsider", routeAdminRole, http.MethodGet, path, nil, nil)
	if outsider.status != http.StatusForbidden || outsider.body["code"] != "backend.workflow.process_access_denied" {
		t.Fatalf("unrelated reader status=%d body=%s", outsider.status, outsider.raw)
	}
}

// routeSimulateCrash rewinds the durable state to the moment just after the
// Action committed: a starting process, an unactivated first route step and a
// pending intent. It is the exact state a crash between commit and activation
// leaves behind.
func routeSimulateCrash(t *testing.T, dbPath, processID string) {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM _workflow_tasks WHERE process_id = ?", []any{processID}},
		{"DELETE FROM _workflow_node_instances WHERE process_id = ?", []any{processID}},
		{"UPDATE _workflow_process_instances SET status = 'starting', current_node_ids_json = '[]' WHERE id = ?", []any{processID}},
		{"UPDATE _workflow_route_steps SET status = 'pending', node_instance_id = '' WHERE process_id = ? AND step_no = 1", []any{processID}},
		{"UPDATE _workflow_executions SET status = 'pending', attempt = 0, lease_owner = '', lease_expires_at = '', next_run_at = NULL WHERE process_id = ?", []any{processID}},
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement.sql, statement.args...); err != nil {
			t.Fatalf("rewind %q: %v", statement.sql, err)
		}
	}
}

func routeDrainWorker(t *testing.T, handler http.Handler) {
	t.Helper()
	response := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodPost, "/workflow/recovery/executions/process?limit=25", nil, map[string]string{"X-Operation-Reason": "Activate the staged approval route after a restart"})
	if response.status != http.StatusOK {
		t.Fatalf("worker drain status=%d body=%s", response.status, response.raw)
	}
}

func TestWorkflowInstanceRouteSurvivesARestartAndKeepsApproving(t *testing.T) {
	temp := t.TempDir()
	dbPath := filepath.Join(temp, "runtime.db")
	manifest := routeWorkflowManifest(t, temp, routeDefaultContract())
	newRuntime := func(t *testing.T) http.Handler {
		return newRouteWorkflowRuntime(t, config.Config{
			AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: dbPath, ManifestPath: manifest, UploadDir: filepath.Join(temp, "uploads"),
		}, routeDefaultContract(), routeStandardUsers()).Routes()
	}
	handler := newRuntime(t)
	submitted := routeSubmit(t, handler, map[string]any{"title": "Restart", "steps": []any{
		map[string]any{"step_key": "first", "assignees": []any{"route_user_a"}},
		map[string]any{"step_key": "second", "assignees": []any{"route_user_b"}},
	}}, nil)
	if submitted.status != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
	}
	output, _ := submitted.body["output"].(map[string]any)
	processID, _ := output["process_id"].(string)
	routeAwaitOpenTask(t, handler, "route_user_a")
	routeSimulateCrash(t, dbPath, processID)

	restarted := newRuntime(t)
	if tasks := routeOpenTasks(t, restarted, "route_user_a"); len(tasks) != 0 {
		t.Fatalf("the rewound process still had tasks: %#v", tasks)
	}
	routeDrainWorker(t, restarted)
	first := routeAwaitOpenTask(t, restarted, "route_user_a")
	firstID, _ := first["id"].(string)
	approved := routeDecide(t, restarted, "route_user_a", firstID, "approve", map[string]any{"comment": "after restart"}, "")
	if approved.status != http.StatusOK || approved.body["status"] != "waiting" {
		t.Fatalf("approval after restart status=%d body=%s", approved.status, approved.raw)
	}
	second := routeAwaitOpenTask(t, restarted, "route_user_b")
	secondID, _ := second["id"].(string)
	completed := routeDecide(t, restarted, "route_user_b", secondID, "approve", nil, "")
	if completed.status != http.StatusOK || completed.body["status"] != "completed" {
		t.Fatalf("final approval status=%d body=%s", completed.status, completed.raw)
	}
	route := routeRequest(t, restarted, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID+"/route", nil, nil)
	steps, _ := route.body["steps"].([]any)
	firstStep, _ := steps[0].(map[string]any)
	secondStep, _ := steps[1].(map[string]any)
	if firstStep["status"] != "approved" || secondStep["status"] != "approved" || firstStep["approved_count"] != float64(1) {
		t.Fatalf("route after the restarted run=%s", route.raw)
	}
}

func TestWorkflowInstanceRouteAcceptsExactlyOneOfTwoConcurrentConfigurations(t *testing.T) {
	handler, _ := routeDeferredRuntime(t)
	submitted := routeDeferredSubmit(t, handler, "Concurrent", map[string]any{
		"step_key": "first", "mode": "quorum", "required_approvals": 2, "assignees": []any{"route_user_a", "route_user_b"},
	})
	if submitted.status != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
	}
	output, _ := submitted.body["output"].(map[string]any)
	processID, _ := output["process_id"].(string)
	firstTask, _ := routeAwaitOpenTask(t, handler, "route_user_a")["id"].(string)
	secondTask, _ := routeAwaitOpenTask(t, handler, "route_user_b")["id"].(string)
	type attempt struct {
		user, task, assignee string
	}
	results := make(chan routeResponse, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for _, candidate := range []attempt{{"route_user_a", firstTask, routeInitiator}, {"route_user_b", secondTask, "route_user_c"}} {
		group.Go(func() {
			<-start
			results <- routeDecide(t, handler, candidate.user, candidate.task, "approve", map[string]any{
				"next_step": map[string]any{"assignee_user_ids": []any{candidate.assignee}},
			}, "")
		})
	}
	close(start)
	group.Wait()
	close(results)
	accepted, conflicts := 0, 0
	for response := range results {
		switch {
		case response.status == http.StatusOK:
			accepted++
		case response.status == http.StatusConflict && response.body["code"] == "backend.workflow.next_step_already_configured":
			conflicts++
		default:
			t.Fatalf("unexpected concurrent outcome status=%d body=%s", response.status, response.raw)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("concurrent configurations accepted=%d conflicts=%d", accepted, conflicts)
	}
	route := routeRequest(t, handler, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID+"/route", nil, nil)
	steps, _ := route.body["steps"].([]any)
	second, _ := steps[1].(map[string]any)
	if second["assignee_count"] != float64(1) {
		t.Fatalf("exactly one configuration must survive: %s", route.raw)
	}
}

func TestWorkflowInstanceRouteRevalidatesAConfiguredApproverAtActivation(t *testing.T) {
	for _, test := range []struct {
		policy string
		status string
	}{{"fail", "configuration_error"}, {"skip_invalid", "waiting"}} {
		t.Run(test.policy, func(t *testing.T) {
			temp := t.TempDir()
			dbPath := filepath.Join(temp, "runtime.db")
			contract := routeDefaultContract()
			contract["revalidate_on_activation"] = test.policy
			contract["max_assignees_per_step"] = 2
			manifest := routeWorkflowManifest(t, temp, contract)
			users := append(routeStandardUsers(), routeWorkflowUser{id: "route_leaver", role: routeApproverRole})
			newRuntime := func(t *testing.T, users []routeWorkflowUser) http.Handler {
				return newRouteWorkflowRuntime(t, config.Config{
					AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: dbPath, ManifestPath: manifest, UploadDir: filepath.Join(temp, "uploads"),
				}, contract, users).Routes()
			}
			handler := newRuntime(t, users)
			steps := []any{
				map[string]any{"step_key": "first", "assignees": []any{"route_user_a"}},
				map[string]any{"step_key": "second", "assignees": []any{"route_leaver", "route_user_b"}},
			}
			submitted := routeSubmit(t, handler, map[string]any{"title": "Revalidate", "steps": steps}, nil)
			if submitted.status != http.StatusOK {
				t.Fatalf("submit status=%d body=%s", submitted.status, submitted.raw)
			}
			output, _ := submitted.body["output"].(map[string]any)
			processID, _ := output["process_id"].(string)
			routeAwaitOpenTask(t, handler, "route_user_a")

			// The configured approver leaves the workspace before the step opens.
			departed := append([]routeWorkflowUser(nil), routeStandardUsers()...)
			departed = append(departed, routeWorkflowUser{id: "route_leaver", role: routeApproverRole, status: "disabled"})
			restarted := newRuntime(t, departed)
			firstID, _ := routeAwaitOpenTask(t, restarted, "route_user_a")["id"].(string)
			approved := routeDecide(t, restarted, "route_user_a", firstID, "approve", nil, "")
			if approved.status != http.StatusOK || approved.body["status"] != test.status {
				t.Fatalf("%s policy status=%d body=%s", test.policy, approved.status, approved.raw)
			}
			if test.policy == "fail" {
				if tasks := routeOpenTasks(t, restarted, "route_user_b"); len(tasks) != 0 {
					t.Fatalf("a failed revalidation still opened the step: %#v", tasks)
				}
			}
			detail := routeRequest(t, restarted, routeInitiator, routeApproverRole, http.MethodGet, "/workflow/processes/"+processID, nil, nil)
			if !strings.Contains(detail.raw, "route_step_revalidated") {
				t.Fatalf("the revalidation was not recorded: %s", detail.raw)
			}
			if test.policy == "skip_invalid" {
				survivor := routeAwaitOpenTask(t, restarted, "route_user_b")
				if survivor["process_id"] != processID {
					t.Fatalf("the surviving approver did not receive the step: %#v", survivor)
				}
				if tasks := routeOpenTasks(t, restarted, "route_leaver"); len(tasks) != 0 {
					t.Fatalf("the departed approver received a task: %#v", tasks)
				}
			}
		})
	}
}
