package composition

import recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"

func initializeWorkflowAutomationAndGovernance(s *runtimeAssembly, deps RuntimeServicesDependencies) {
	s.workflowApplicationService = assembleWorkflowApplication(s)
	schedulerRuntime := newScheduledWorkflowRuntimeAdapter(s)
	s.schedulerService = newSchedulerApplicationService(schedulerRuntime, s.workerDependencies)
	s.schedulerService.UseDefinitionSource(schedulerApplicationDefinitionSource{definitions: s.metadataDefinitions, authored: s.schedulerDefinitions})
	s.recordTimerService = recordtimerapplication.NewRecordTimerApplicationServiceWithWorker(s, newRecordTimerTargetRuntimeAdapter(s), s.recordRepo, s.workerDependencies)
	s.authoringCapabilities = newCapabilityAuthoringApplicationService(s)
	s.businessReferences = assembleChangePlanReferenceApplication(s, businessReferenceRuntimeAdapter{records: s, workflows: s.workflowApplicationService}, s.businessEvidenceRepo)
	s.applicationSchemaService = assembleApplicationSchema(s)
	s.automationApplicationService = assembleAutomationApplication(s)
}
