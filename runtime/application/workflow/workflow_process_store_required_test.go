package workflow

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowProcessEngineStartRequiresProcessStore(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{
		Key: "order.follow_up",
		Graph: &definitionmodel.WorkflowGraphSchema{
			Version: 2,
			Nodes:   []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}},
		},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		WorkspaceID: "workspace",
		UserID:      "user"},
	}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}},
	)

	for name, engine := range map[string]*WorkflowProcessEngine{
		"nil engine":  nil,
		"nil runtime": {},
		"nil store":   NewWorkflowProcessEngine(WorkflowDependencies{}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := engine.Start(t.Context(), workflow, nil, principal); apperror.CodeOf(err) != "backend.internal" {
				t.Fatalf("Start() error code = %q, want backend.internal (err=%v)", apperror.CodeOf(err), err)
			}
		})
	}
}

func TestSyncWorkflowExecutionAllowsMissingWorkerStore(t *testing.T) {
	for name, service := range map[string]*WorkflowApplicationService{
		"nil service": nil,
		"nil store":   {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := service.syncWorkflowExecutionWithProcess(t.Context(), workflowmodel.WorkflowProcessInstance{ID: "process"}, nil); err != nil {
				t.Fatalf("syncWorkflowExecutionWithProcess() error = %v, want nil", err)
			}
		})
	}
}
