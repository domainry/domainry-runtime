package composition

import recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"

func initializeWorkflowAutomationAndGovernance(s *runtimeAssembly, deps RuntimeServicesDependencies) {
	// Workflow dependencies capture the timer port by value, so construct it
	// before the workflow application takes its dependency snapshot.
	s.recordTimerService = recordtimerapplication.NewRecordTimerApplicationServiceWithWorker(s, newRecordTimerTargetRuntimeAdapter(s), s.recordRepo, s.workerDependencies)
	s.workflowApplicationService = assembleWorkflowApplication(s)
	schedulerRuntime := newScheduledWorkflowRuntimeAdapter(s)
	s.schedulerService = newSchedulerApplicationService(schedulerRuntime, s.workerDependencies)
	s.schedulerService.UseDefinitionSource(schedulerApplicationDefinitionSource{definitions: s.metadataDefinitions, authored: s.schedulerDefinitions})
	s.authoringCapabilities = newCapabilityAuthoringApplicationService(s)
	s.businessReferences = assembleChangePlanReferenceApplication(s, businessReferenceRuntimeAdapter{records: s, workflows: s.workflowApplicationService}, s.businessEvidenceRepo)
	s.applicationSchemaService = assembleApplicationSchema(s)
	s.automationApplicationService = assembleAutomationApplication(s)
}
