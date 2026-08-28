package composition

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
)

// Keep Composition adapters checked against the owner ports they bridge. These
// assertions make wiring drift fail at compile time during the test build.
var (
	_ identitysdk.Directory = compositionIdentityDirectory{}

	_ workflowapplication.WorkflowRegistry       = runtimeWorkflowRegistry{}
	_ workflowapplication.WorkflowSchemaProvider = runtimeWorkflowSchemaProvider{}
	_ workflowapplication.WorkflowScheduler      = runtimeWorkflowScheduler{}

	_ metadataapplication.LifecycleRuntime = metadataLifecycleRuntimeAdapter{}

	_ changeplanapplication.ReferenceRuntime       = businessReferenceRuntimePortAdapter{}
	_ changeplanapplication.FrontendSnapshotSource = businessReferenceFrontendPortAdapter{}
	_ BusinessReferenceRuntimeProvider             = businessReferenceRuntimeAdapter{}

	_ schedulerapplication.SchedulerOperationRuntime = schedulerOperationRuntimeAdapter{}
	_ schedulerapplication.RecordMutationRuntime     = schedulerOperationRuntimeAdapter{}
)
