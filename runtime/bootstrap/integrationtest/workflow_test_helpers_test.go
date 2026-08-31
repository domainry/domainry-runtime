package integrationtest

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func runWorkflowProcess(t *testing.T, store *persistence.RuntimeStore, records *RuntimeServices, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	t.Helper()
	run, err := records.Applications().Workflows.RunWorkflow(t.Context(), workflowKey, payload, principal)
	if run.Execution.ProcessID == "" {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	process, _, getErr := workflowProcessStore(store).GetProcess(t.Context(), "workspace-primary", run.Execution.ProcessID)
	if err == nil {
		err = getErr
	}
	return process, err
}
