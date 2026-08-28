package workflow

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type workflowProcessAuthorizationProbe struct {
	workflowcontract.WorkflowProcessStore
	calls *int
}

func (p workflowProcessAuthorizationProbe) ListProcesses(context.Context, string, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	*p.calls++
	return nil, nil
}

type workflowWorkerAuthorizationProbe struct {
	workflowcontract.WorkflowWorkerStore
	calls *int
}

func (p workflowWorkerAuthorizationProbe) ListExecutions(context.Context, string, int) ([]workflowmodel.WorkflowExecution, error) {
	*p.calls++
	return nil, nil
}

func TestWorkflowApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	calls := 0
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Processes: workflowProcessAuthorizationProbe{calls: &calls},
		Workers:   workflowWorkerAuthorizationProbe{calls: &calls},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})

	checks := []func() error{
		func() error {
			_, err := service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{})
			return err
		},
		func() error { _, err := service.WorkflowExecutions(t.Context(), principal, "", "", 10); return err },
		func() error { _, err := service.ProcessDueWorkflowExecutions(t.Context(), 10, principal); return err },
		func() error {
			return service.InitializePublishedWorkflowDefinitions(t.Context(), nil, principalmodel.SystemScope{})
		},
	}
	for index, check := range checks {
		code := apperror.CodeOf(check())
		if code != "backend.workspace_scope_required" && code != "backend.system_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	service.ExecuteCommittedWorkflowIntents(t.Context(), []workflowmodel.WorkflowExecution{{ID: "intent-1"}}, principal)
	if calls != 0 {
		t.Fatalf("repository called before workflow scope authorization: %d", calls)
	}
}
