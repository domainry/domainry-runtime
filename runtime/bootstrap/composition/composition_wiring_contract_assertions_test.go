package composition

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
)

// Keep Composition adapters checked against the owner ports they bridge. These
// assertions make wiring drift fail at compile time during the test build.
var (
	_ identitysdk.Projection = compositionIdentityProjection{}

	_ workflowapplication.WorkflowRegistry       = runtimeWorkflowRegistry{}
	_ workflowapplication.WorkflowSchemaProvider = runtimeWorkflowSchemaProvider{}

	_ appschemaapplication.LifecycleRuntime = applicationSchemaLifecycleRuntimeAdapter{}

	_ changeplanapplication.ReferenceRuntime = businessReferenceRuntimePortAdapter{}
	_ BusinessReferenceRuntimeProvider       = businessReferenceRuntimeAdapter{}

	_ dispatchapplication.WorkflowTargetRuntime = scheduledWorkflowRuntimeAdapter{}
)
