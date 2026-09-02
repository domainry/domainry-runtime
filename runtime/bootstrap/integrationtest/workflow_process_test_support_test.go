package integrationtest

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordtimerprojection "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/projection"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

func newWorkflowProcessTestService(t *testing.T, store *persistence.RuntimeStore, workflow definitionmodel.WorkflowSchema, identityDirectory identitysdk.Directory) *RuntimeServices {
	t.Helper()
	return runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "workflow-process-test", TemplateVersion: "1", Name: "Workflow Process Test",
		Objects: recordtimerprojection.RecordTimerSystemObjects(), Workflows: []definitionmodel.WorkflowSchema{workflow},
		Integrations: connectormodel.IntegrationSchema{}, Store: store, IdentityDirectory: identityDirectory,
		WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store),
		WorkflowDecisions: workflowpersistence.NewWorkflowDecisionStore(store),
		WorkflowWorker:    workflowpersistence.NewWorkflowWorkerStore(store),
	})
}
