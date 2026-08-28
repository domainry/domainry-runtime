package workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowSurfaceDTOsDoNotCrossLeakBusinessAndOperationsFields(t *testing.T) {
	business, err := json.Marshal(BusinessWorkflowProcessDTO{
		ID: "process", WorkflowKey: "approval", Status: "waiting", BusinessOutcome: "approved",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"definition_snapshot", "definition_hash", "variables", "error_code", "lease_owner", "fencing_token"} {
		if strings.Contains(string(business), forbidden) {
			t.Fatalf("Business workflow DTO leaked %s: %s", forbidden, business)
		}
	}
	operations, err := json.Marshal(OpsWorkflowExecutionDTO{
		ID: "execution", WorkflowKey: "approval", Status: "retrying", LeaseOwner: "worker-1", FencingToken: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"payload"`, `"action"`, `"result"`, `"actor_id"`, `"run_as"`} {
		if strings.Contains(string(operations), forbidden) {
			t.Fatalf("Ops workflow DTO leaked %s: %s", forbidden, operations)
		}
	}
	if !strings.Contains(string(operations), `"lease_owner":"worker-1"`) {
		t.Fatalf("Ops workflow DTO omitted technical recovery state: %s", operations)
	}

	detail, err := json.Marshal(ProjectBusinessWorkflowProcessDetail(WorkflowProcessDetail{
		Process: workflowmodel.WorkflowProcessInstance{
			ID: "process", WorkflowKey: "approval", WorkflowName: "Approval", InitiatorID: "user", Status: "waiting",
			DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{
				ID: "approve", Name: "Manager approval", Contract: &definitionmodel.WorkflowNodeContract{
					Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "refund.execute"},
				},
			}}}},
		},
		Nodes: []workflowmodel.WorkflowNodeInstance{{
			ID: "node", NodeID: "approve", NodeType: "approval", Status: "waiting",
			Input: map[string]any{"secret": "must-not-project"}, Output: map[string]any{"secret": "must-not-project"},
		}},
		Tasks: []workflowmodel.WorkflowTask{{
			ID: "task", ProcessID: "process", NodeInstanceID: "node", NodeID: "approve", Title: "Approve",
			AssigneeRoleKey: "manager", Sequence: 1, Status: "open",
		}},
		Events: []workflowmodel.WorkflowProcessEvent{{
			ID: "event", Event: "task_created", Summary: "Task created",
			Metadata: map[string]any{"secret": "must-not-project"},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"node_id":"approve"`, `"name":"Manager approval"`, `"action_key":"refund.execute"`, `"assignee_role_key":"manager"`, `"event":"task_created"`} {
		if !strings.Contains(string(detail), required) {
			t.Fatalf("Business workflow detail omitted %s: %s", required, detail)
		}
	}
	for _, forbidden := range []string{"must-not-project", `"input"`, `"output"`, `"metadata"`, `"resolver_snapshot"`} {
		if strings.Contains(string(detail), forbidden) {
			t.Fatalf("Business workflow detail leaked %s: %s", forbidden, detail)
		}
	}

	projectedDetail, err := json.Marshal(ProjectBusinessWorkflowProcessDetail(WorkflowProcessDetail{
		Process: workflowmodel.WorkflowProcessInstance{
			ID: "projected", WorkflowKey: "approval", WorkflowName: "Approval", InitiatorID: "user", Status: "running",
			DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{
				ID: "execute", Type: "action", Name: "Execute refund", Config: map[string]any{"action_key": "refund.execute"},
			}}}},
		},
		Nodes: []workflowmodel.WorkflowNodeInstance{{ID: "node", NodeID: "execute", NodeType: "action", Status: "running"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectedDetail), `"action_key":"refund.execute"`) {
		t.Fatalf("Business workflow detail omitted projected Action key: %s", projectedDetail)
	}
}

func TestOpsWorkflowProcessSurfaceRequiresExactPermissionsAndProjectsTechnicalDetail(t *testing.T) {
	workspaceAdmin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	service := NewWorkflowApplicationService(WorkflowDependencies{})
	if _, err := service.OpsWorkflowProcesses(t.Context(), principalmodel.Principal{}, workflowmodel.WorkflowProcessFilter{}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("unknown principal list authorization=%v", err)
	}
	if _, err := service.OpsWorkflowProcesses(t.Context(), workspaceAdmin, workflowmodel.WorkflowProcessFilter{}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace.admin list authorization=%v", err)
	}
	if _, err := service.OpsWorkflowProcess(t.Context(), "process", workspaceAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace.admin detail authorization=%v", err)
	}
	if _, err := service.RetryOpsWorkflowProcessWithKey(t.Context(), "process", "key", workspaceAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace.admin retry authorization=%v", err)
	}
	if _, err := service.ResolveOpsWorkflowProcessFailure(t.Context(), "process", "note", workspaceAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace.admin resolve authorization=%v", err)
	}

	process := workflowmodel.WorkflowProcessInstance{
		ID: "process", WorkspaceID: "workspace", WorkflowKey: "approval", InitiatorID: "initiator",
		Status: "failed", ObjectKey: "request", RecordID: "request-1", CreatedAt: "2026-07-26T00:00:00Z", UpdatedAt: "2026-07-26T00:01:00Z",
		Variables: map[string]any{"secret_business_value": "must-not-project"},
		Result:    map[string]any{"retry_count": 2, "payload": "must-not-project"},
	}
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process},
			nodes: map[string][]workflowmodel.WorkflowNodeInstance{
				"process": {{ID: "node-1", NodeID: "approve", NodeType: "action", Status: "failed", ErrorCode: "action.failed", StartedAt: "2026-07-26T00:00:30Z"}},
			},
		},
		events: []workflowmodel.WorkflowProcessEvent{{ID: "event-1", Event: "node_failed", ActorID: "worker", Summary: "failed", CreatedAt: "2026-07-26T00:01:00Z"}},
	}
	reader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workflow.process.read"}})
	detail, err := workflowProcessQueryService(store, nil).OpsWorkflowProcess(t.Context(), " process ", reader)
	if err != nil || detail.Process.ID != "process" || detail.Process.ObjectKey != "request" || len(detail.Nodes) != 1 || len(detail.Events) != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret_business_value", "must-not-project", `"variables"`, `"result"`, `"tasks"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("Ops detail leaked %s: %s", forbidden, raw)
		}
	}
}

func TestBusinessTeamWorkflowTasksAuthorizationDefaultsLimitsAndRepositoryFailure(t *testing.T) {
	store := &workflowProcessStoreEdgeStub{tasks: []workflowmodel.WorkflowTask{{
		ID: "task-1", ProcessID: "process-1", Title: "Approve", AssigneeUserID: "user-1", Status: "open",
	}}}
	service := workflowProcessQueryService(store, nil)
	if _, err := service.BusinessTeamWorkflowTasks(t.Context(), principalmodel.Principal{}, "", 0); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal error=%v", err)
	}
	denied := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}
	if _, err := service.BusinessTeamWorkflowTasks(t.Context(), denied, "", 0); apperror.CodeOf(err) != "backend.workflow.team_tasks_permission_required" {
		t.Fatalf("permission error=%v", err)
	}
	reader := denied
	reader = workflowPrincipalWithPermissions(reader, "workflow.process.read")
	tasks, err := service.BusinessTeamWorkflowTasks(t.Context(), reader, "", 0)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "task-1" {
		t.Fatalf("default tasks=%+v err=%v", tasks, err)
	}
	tasks, err = service.BusinessTeamWorkflowTasks(t.Context(), reader, "completed", 501)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("limited tasks=%+v err=%v", tasks, err)
	}
	store.listTasksErr = errors.New("task store unavailable")
	if _, err := service.BusinessTeamWorkflowTasks(t.Context(), reader, "open", 10); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("repository error=%v", err)
	}
}

func TestRetryOpsWorkflowProcessSuccessProjection(t *testing.T) {
	process := workflowMutationProcess("failed")
	process.WorkspaceID = "workspace"
	process.Result = nil
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process},
			nodes: map[string][]workflowmodel.WorkflowNodeInstance{
				"process": {{NodeID: "failed", Status: "failed"}},
			},
		},
	}
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	operator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workflow.process.operate"}})
	projected, err := workflowProcessMutationService(store, worker, nil).RetryOpsWorkflowProcessWithKey(t.Context(), "process", "retry-key", operator)
	if err != nil || projected.ID != "process" || projected.Status != "completed" || projected.RetryCount != 1 {
		t.Fatalf("projected=%+v err=%v", projected, err)
	}
}

func TestWorkflowSurfaceProjectionWrappersCoverSuccessAndServiceFailures(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{
		ID: "process-1", WorkspaceID: "workspace", WorkflowKey: "approval", WorkflowName: "Approval",
		InitiatorID: "user", Status: "waiting", Result: map[string]any{"retry_count": 2},
	}
	task := workflowmodel.WorkflowTask{
		ID: "task-1", ProcessID: process.ID, Title: "Approve", AssigneeUserID: "user", Status: "open",
	}
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process},
			nodes:     map[string][]workflowmodel.WorkflowNodeInstance{process.ID: {}},
		},
		listProcesses: []workflowmodel.WorkflowProcessInstance{process},
		tasks:         []workflowmodel.WorkflowTask{task},
	}
	service := workflowProcessQueryService(store, nil)
	reader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workflow.process.read"}})

	tasks, err := service.BusinessWorkflowTasks(t.Context(), reader, "open", 10)
	if err != nil || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("business tasks=%+v err=%v", tasks, err)
	}
	store.listTasksErr = errors.New("tasks unavailable")
	if _, err := service.BusinessWorkflowTasks(t.Context(), reader, "open", 10); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("business task error=%v", err)
	}
	store.listTasksErr = nil

	processes, err := service.BusinessWorkflowProcesses(t.Context(), reader, workflowmodel.WorkflowProcessFilter{})
	if err != nil || len(processes) != 1 || processes[0].ID != process.ID {
		t.Fatalf("business processes=%+v err=%v", processes, err)
	}
	opsProcesses, err := service.OpsWorkflowProcesses(t.Context(), reader, workflowmodel.WorkflowProcessFilter{})
	if err != nil || len(opsProcesses) != 1 || opsProcesses[0].RetryCount != 2 {
		t.Fatalf("ops processes=%+v err=%v", opsProcesses, err)
	}
	store.listProcessesErr = errors.New("processes unavailable")
	if _, err := service.BusinessWorkflowProcesses(t.Context(), reader, workflowmodel.WorkflowProcessFilter{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("business process error=%v", err)
	}
	if _, err := service.OpsWorkflowProcesses(t.Context(), reader, workflowmodel.WorkflowProcessFilter{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("ops process error=%v", err)
	}
	store.listProcessesErr = nil
	store.getProcessErr = errors.New("process unavailable")
	if _, err := service.OpsWorkflowProcess(t.Context(), process.ID, reader); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("ops detail error=%v", err)
	}

	worker := &workflowListWorkerEdgeStub{
		workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}},
		list: []workflowmodel.WorkflowExecution{{
			ID: "execution-1", WorkflowKey: "approval", Status: "queued", ProcessID: process.ID, Attempt: 1, MaxAttempts: 3,
		}},
	}
	executionService := NewWorkflowApplicationService(WorkflowDependencies{
		Workers: worker, Processes: store, Schema: workflowSchemaProviderEdgeStub{},
	})
	executionReader := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"ops.workflow.read"}})
	executions, err := executionService.OpsWorkflowExecutions(t.Context(), executionReader, "", "", 10)
	if err != nil || len(executions) != 1 || executions[0].ID != "execution-1" {
		t.Fatalf("ops executions=%+v err=%v", executions, err)
	}
	worker.listErr = errors.New("executions unavailable")
	if _, err := executionService.OpsWorkflowExecutions(t.Context(), executionReader, "", "", 10); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("ops execution error=%v", err)
	}

	_ = ProjectBusinessWorkflowProcess(process)
	_ = ProjectBusinessWorkflowRun(workflowmodel.WorkflowRunResult{WorkflowKey: "approval", Execution: workflowmodel.WorkflowExecution{ProcessID: process.ID}})
	_ = ProjectOpsWorkflowExecution(workflowmodel.WorkflowExecution{ID: "execution-1"})
	_ = ProjectOpsWorkflowProcess(process)
}

func TestOpsWorkflowMutationWrappersPropagateAuthorizedServiceFailures(t *testing.T) {
	operator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workflow.process.operate"}})
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{},
			nodes:     map[string][]workflowmodel.WorkflowNodeInstance{},
		},
	}
	service := workflowProcessMutationService(store, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, nil)
	if _, err := service.RetryOpsWorkflowProcessWithKey(t.Context(), "missing", "retry-key", operator); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("retry error=%v", err)
	}
	if _, err := service.ResolveOpsWorkflowProcessFailure(t.Context(), "missing", "note", operator); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("resolve error=%v", err)
	}
	process := workflowMutationProcess("failed")
	process.WorkspaceID = "workspace"
	successStore := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process},
			nodes:     map[string][]workflowmodel.WorkflowNodeInstance{},
		},
	}
	service = workflowProcessMutationService(
		successStore,
		&workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}},
		&workflowStateDecisionEdgeStub{},
	)
	resolved, err := service.ResolveOpsWorkflowProcessFailure(t.Context(), "process", "resolved by operator", operator)
	if err != nil || resolved.ID != "process" || resolved.Status != "resolved" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}
