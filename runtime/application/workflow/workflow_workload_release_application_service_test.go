package workflow

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type workflowWorkloadIdentityTestStub struct {
	requests     []identitysdk.ApplyWorkflowWorkloadBindingsRequest
	mutateResult func(*identitysdk.ApplyWorkflowWorkloadBindingsResult)
}

func (s *workflowWorkloadIdentityTestStub) ApplyWorkflowWorkloadBindings(_ context.Context, request identitysdk.ApplyWorkflowWorkloadBindingsRequest) (identitysdk.ApplyWorkflowWorkloadBindingsResult, error) {
	s.requests = append(s.requests, request)
	result := identitysdk.ApplyWorkflowWorkloadBindingsResult{}
	for _, spec := range request.Bindings {
		result.Bindings = append(result.Bindings, identitysdk.WorkflowWorkloadBinding{
			Application: request.Application, SubjectID: identitysdk.WorkflowWorkloadSubjectID(spec.WorkflowKey), WorkflowKey: spec.WorkflowKey,
			DefinitionVersionID: spec.DefinitionVersionID, DefinitionVersion: spec.DefinitionVersion, RoleKey: spec.RoleKey,
			ActionKeys: append([]string(nil), spec.ActionKeys...), ReleaseID: request.ReleaseID, ReleaseDigest: request.ReleaseDigest,
			Status: identitysdk.WorkflowWorkloadBindingActive,
		})
	}
	if s.mutateResult != nil {
		s.mutateResult(&result)
	}
	return result, nil
}

func (*workflowWorkloadIdentityTestStub) GetWorkflowWorkloadBinding(context.Context, identitysdk.GetWorkflowWorkloadBindingRequest) (identitysdk.WorkflowWorkloadBinding, error) {
	return identitysdk.WorkflowWorkloadBinding{}, nil
}

type workflowWorkloadPrincipalTestStub struct {
	requests    []identitysdk.PrincipalResolutionRequest
	permissions []string
	mutate      func(*identitysdk.PrincipalResolution)
}

func (s *workflowWorkloadPrincipalTestStub) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	s.requests = append(s.requests, request)
	principal := workflowPrincipalWithPermissions(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: requestcontext.WorkspaceID(ctx), UserID: string(request.SubjectID), RoleKey: request.RoleKey,
	}}, s.permissions...)
	resolution := workflowPrincipalResolution(principal)
	if request.Workload != nil {
		resolution.Principal.Workload = &identitysdk.WorkflowWorkloadPrincipalContext{
			WorkflowKey: request.Workload.WorkflowKey, DefinitionVersionID: request.Workload.DefinitionVersionID, DefinitionVersion: request.Workload.DefinitionVersion,
			ReleaseID: request.Workload.ReleaseID, ReleaseDigest: request.Workload.ReleaseDigest, TaskID: request.Workload.TaskID,
			SourceEventID: request.Workload.SourceEventID, InitiatorSubjectID: request.Workload.InitiatorSubjectID,
		}
	}
	if s.mutate != nil {
		s.mutate(&resolution)
	}
	return resolution, nil
}

func workflowWorkloadReleaseTestDefinition() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{
		DefinitionVersionID: "version-2", PublishedVersion: 2, Key: "settlement", Name: "Settlement", Enabled: true, RunAs: "settlement_service",
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "start", Type: "trigger"},
			{ID: "settle", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "payment.settle"}}},
			{ID: "notify", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "payment.notify"}}},
			{ID: "approve", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{ReminderActionKey: "payment.remind"}}},
		}},
	}
}

func TestWorkflowWorkloadReleaseSynchronizesAndCarriesExecutionLineage(t *testing.T) {
	workload := &workflowWorkloadIdentityTestStub{}
	principals := &workflowWorkloadPrincipalTestStub{permissions: []string{"payment.notify", "payment.remind", "payment.settle"}}
	state := NewWorkflowWorkloadReleaseState()
	application := identitysdk.ApplicationScope{WorkspaceID: "workspace-1", ApplicationKey: "runtime"}
	state.configure(application)
	service := &WorkflowApplicationService{workloads: workload, workloadApplication: application, workloadReleases: state, principals: principals}
	workflow := workflowWorkloadReleaseTestDefinition()

	if err := service.synchronizeWorkflowWorkloadBindings(t.Context(), map[string]definitionmodel.WorkflowSchema{workflow.Key: workflow}); err != nil {
		t.Fatal(err)
	}
	if len(workload.requests) != 1 || len(workload.requests[0].Bindings) != 1 {
		t.Fatalf("apply requests=%+v", workload.requests)
	}
	if got, want := workload.requests[0].Bindings[0].ActionKeys, []string{"payment.notify", "payment.remind", "payment.settle"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("action keys=%v want=%v", got, want)
	}
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-7"}, RequestID: "request-1", CorrelationID: "correlation-1"}
	resolved, err := NewWorkflowPrincipalResolver(principals, state).ResolveWorkflowPrincipalForExecution(t.Context(), workflow, initiator, WorkflowPrincipalExecutionContext{TaskID: "task-9", SourceEventID: "event-3"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Workload == nil || resolved.Workload.TaskID != "task-9" || resolved.Workload.SourceEventID != "event-3" || resolved.Workload.InitiatorSubjectID != "user-7" || resolved.RequestID != "request-1" || resolved.CorrelationID != "correlation-1" {
		t.Fatalf("resolved lineage=%+v", resolved)
	}
	workerInitiator := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "runtime-target-executor"}}
	workerResolved, err := NewWorkflowPrincipalResolver(principals, state).ResolveWorkflowPrincipalForExecution(t.Context(), workflow, workerInitiator, WorkflowPrincipalExecutionContext{TaskID: "process-4:settle", SourceEventID: "scheduler-run-4"})
	if err != nil {
		t.Fatal(err)
	}
	if workerResolved.RequestID != "process-4:settle" || workerResolved.Workload == nil || workerResolved.Workload.InitiatorSubjectID != "runtime-target-executor" {
		t.Fatalf("durable worker invocation identity=%+v", workerResolved)
	}

	old := workflow
	old.DefinitionVersionID, old.PublishedVersion = "version-1", 1
	if _, err := NewWorkflowPrincipalResolver(principals, state).ResolveWorkflowPrincipal(t.Context(), old, initiator); err == nil {
		t.Fatal("replaced workflow version resolved with the active workload release")
	}
	wrongRole := &workflowWorkloadPrincipalTestStub{
		permissions: []string{"payment.notify", "payment.remind", "payment.settle"},
		mutate: func(resolution *identitysdk.PrincipalResolution) {
			resolution.Principal.RoleKey = "other_service"
		},
	}
	if _, err := NewWorkflowPrincipalResolver(wrongRole, state).ResolveWorkflowPrincipalForExecution(t.Context(), workflow, initiator, WorkflowPrincipalExecutionContext{TaskID: "task-9", SourceEventID: "event-3"}); apperror.CodeOf(err) != "backend.workflow.execution_principal_mismatch" {
		t.Fatalf("mismatched execution principal was accepted: %v", err)
	}
}

func TestManagedWorkloadSharesAtomicReleaseAndResolvesExactAuthorizationVersion(t *testing.T) {
	workload := &workflowWorkloadIdentityTestStub{}
	principals := &workflowWorkloadPrincipalTestStub{permissions: []string{"order.expire"}}
	state := NewWorkflowWorkloadReleaseState()
	application := identitysdk.ApplicationScope{WorkspaceID: "workspace-1", ApplicationKey: "runtime"}
	state.configure(application)
	service := &WorkflowApplicationService{workloads: workload, workloadApplication: application, workloadReleases: state, principals: principals}
	restore, err := service.ReplaceManagedWorkloadBindings([]ManagedWorkloadBinding{{
		WorkloadKey: "scheduler:expire-orders", DefinitionVersionID: strings.Repeat("a", 64), DefinitionVersion: 1,
		RoleKey: "order_automation", ActionKeys: []string{"order.expire"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.synchronizeWorkflowWorkloadBindings(t.Context(), map[string]definitionmodel.WorkflowSchema{}); err != nil {
		t.Fatal(err)
	}
	if len(workload.requests) != 1 || len(workload.requests[0].Bindings) != 1 || workload.requests[0].Bindings[0].WorkflowKey != "scheduler:expire-orders" {
		t.Fatalf("release requests=%+v", workload.requests)
	}
	principal, err := service.ResolveManagedWorkloadPrincipal(t.Context(), ManagedWorkloadExecution{
		WorkloadKey: "scheduler:expire-orders", DefinitionVersionID: strings.Repeat("a", 64), DefinitionVersion: 1, RoleKey: "order_automation",
		ExecutionID: "run-7", SourceEventID: "run-7", IdempotencyKey: "window-7",
	})
	if err != nil || principal.UserID != "workflow:scheduler:expire-orders" || principal.RoleKey != "order_automation" || principal.RequestID != "window-7" || principal.Workload == nil || principal.Workload.TaskID != "run-7" {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	if _, err := service.ResolveManagedWorkloadPrincipal(t.Context(), ManagedWorkloadExecution{WorkloadKey: "scheduler:expire-orders", DefinitionVersionID: strings.Repeat("b", 64), DefinitionVersion: 1, RoleKey: "order_automation"}); apperror.CodeOf(err) != "backend.workload.release_not_active" {
		t.Fatalf("stale authorization version err=%v", err)
	}
	restore()
	if got := state.supplementalBindings(); len(got) != 0 {
		t.Fatalf("restore retained supplemental bindings: %+v", got)
	}
}

func TestWorkflowWorkloadReleaseRejectsIncompleteRolePermissionAndRestoresPreviousRelease(t *testing.T) {
	workload := &workflowWorkloadIdentityTestStub{}
	principals := &workflowWorkloadPrincipalTestStub{permissions: []string{"payment.settle"}}
	state := NewWorkflowWorkloadReleaseState()
	application := identitysdk.ApplicationScope{WorkspaceID: "workspace-1", ApplicationKey: "runtime"}
	state.configure(application)
	service := &WorkflowApplicationService{workloads: workload, workloadApplication: application, workloadReleases: state, principals: principals}

	err := service.synchronizeWorkflowWorkloadBindings(t.Context(), map[string]definitionmodel.WorkflowSchema{"settlement": workflowWorkloadReleaseTestDefinition()})
	if err == nil {
		t.Fatal("release with missing action permissions was accepted")
	}
	if len(workload.requests) != 2 || len(workload.requests[1].Bindings) != 0 {
		t.Fatalf("candidate was not compensated with the previous empty release: %+v", workload.requests)
	}
}

func TestWorkflowWorkloadReleaseRejectsTamperedControlPlaneResultsBeforeWorkerStartup(t *testing.T) {
	for _, test := range []struct {
		name            string
		mutateBinding   func(*identitysdk.WorkflowWorkloadBinding)
		mutatePrincipal func(*identitysdk.PrincipalResolution)
		missingBinding  bool
		wantCode        string
	}{
		{name: "missing binding", missingBinding: true, wantCode: "backend.workflow.workload_release_incomplete"},
		{name: "wrong role", mutateBinding: func(binding *identitysdk.WorkflowWorkloadBinding) { binding.RoleKey = "other_service" }, wantCode: "backend.workflow.workload_release_mismatch"},
		{name: "cross workspace", mutateBinding: func(binding *identitysdk.WorkflowWorkloadBinding) { binding.Application.WorkspaceID = "workspace-2" }, wantCode: "backend.workflow.workload_release_mismatch"},
		{name: "tampered digest", mutateBinding: func(binding *identitysdk.WorkflowWorkloadBinding) { binding.ReleaseDigest = strings.Repeat("f", 64) }, wantCode: "backend.workflow.workload_release_mismatch"},
		{name: "disabled role", mutatePrincipal: func(resolution *identitysdk.PrincipalResolution) { resolution.Principal.Known = false }, wantCode: "backend.workflow.workload_preflight_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workload := &workflowWorkloadIdentityTestStub{mutateResult: func(result *identitysdk.ApplyWorkflowWorkloadBindingsResult) {
				if test.missingBinding {
					result.Bindings = nil
					return
				}
				if len(result.Bindings) > 0 && test.mutateBinding != nil {
					test.mutateBinding(&result.Bindings[0])
				}
			}}
			principals := &workflowWorkloadPrincipalTestStub{
				permissions: []string{"payment.notify", "payment.remind", "payment.settle"},
				mutate:      test.mutatePrincipal,
			}
			state := NewWorkflowWorkloadReleaseState()
			application := identitysdk.ApplicationScope{WorkspaceID: "workspace-1", ApplicationKey: "runtime"}
			state.configure(application)
			service := &WorkflowApplicationService{workloads: workload, workloadApplication: application, workloadReleases: state, principals: principals}

			err := service.synchronizeWorkflowWorkloadBindings(t.Context(), map[string]definitionmodel.WorkflowSchema{"settlement": workflowWorkloadReleaseTestDefinition()})
			if err == nil || !strings.Contains(err.Error(), test.wantCode) {
				t.Fatalf("tampered release error=%v want code=%s", err, test.wantCode)
			}
			if len(workload.requests) != 2 || len(workload.requests[1].Bindings) != 0 {
				t.Fatalf("startup preflight did not restore the previous empty release: %+v", workload.requests)
			}
			if _, active := state.Resolution(workflowWorkloadReleaseTestDefinition()); active {
				t.Fatal("rejected startup preflight activated a workload release")
			}
		})
	}
}
