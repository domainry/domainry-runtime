package policy

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// SchedulerAuthoringDomain publishes Scheduler-owned definition and command contracts.
// Persisted definitions, compositional schedule fragments, and runtime commands are
// deliberately separate capabilities because they have different HTTP envelopes.
func SchedulerAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "scheduler", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{
		schedulerBusinessJobAuthoringCapability(),
		schedulerScheduleAuthoringCapability(),
		schedulerCommandAuthoringCapability("scheduler.job.simulate", "business_schedule_simulation", "definition_id", "POST /scheduler/jobs/{definitionID}/simulate", false),
		schedulerCommandAuthoringCapability("scheduler.job.run", "business_schedule_execution", "definition_id", "POST /operations/scheduler/definitions/{definitionID}/run", true),
		schedulerCommandAuthoringCapability("scheduler.run.retry", "business_administrator_recovery", "run_id", "POST /scheduler/runs/{runID}/retry", true),
		schedulerCommandAuthoringCapability("scheduler.run.cancel", "business_administrator_recovery", "run_id", "POST /scheduler/runs/{runID}/cancel", true),
		schedulerResolveDeadLetterAuthoringCapability(),
	}}
}

func schedulerBusinessJobAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := schedulerBusinessJobParameters()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "scheduler.business_job", Status: "supported", Lifecycle: "business_schedule",
		SystemDraftResourceType: "scheduler",
		Parameters:              parameters, Requires: []string{"scheduler.schedule"}, Permissions: []string{"scheduler.definition.read", "scheduler.definition.write"},
		AuditEvents: []string{"business_change_plan.item_applied"}, ValidationEndpoint: "POST /tenant-admin/change-plans/validate", PreviewEndpoint: "POST /scheduler/jobs/preview",
		ConfigurationRoutes: append(appschemacontract.VersionedApplicationDefinitionRoutes("scheduler"), "POST /metadata/definitions/scheduler/{resourceKey}/validate", "GET /scheduler/job-definitions/{definitionID}", "GET /scheduler/job-definitions/{definitionID}/versions", "GET /tenant-admin/change-plans/{planID}", "PUT /tenant-admin/change-plans/{planID}", "POST /tenant-admin/change-plans/validate", "POST /tenant-admin/change-plans/apply"), ResourceKeyPathParameter: "definitionID",
		ResourceOperations: appschemacontract.VersionedApplicationDefinitionOperations("scheduler"),
		FrontendSupportKey: "scheduler.job.editor.v1", InputSchema: schedulerObjectSchema(parameters), OutputSchema: schedulerJobRecordOutputSchema(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "definition_id", JSONPointer: "/id", Type: "record_id", VisibleTo: "subsequent_capability_calls"}, {Name: "job_key", JSONPointer: "/data/key", Type: "scheduler_job_key", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "scheduler_target_key", InputJSONPointer: "/target_key", ScopeFrom: "/target_type", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/scheduler_target_key"}, {Kind: "object_key", InputJSONPointer: "/target_object", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"}, {Kind: "role_key", InputJSONPointer: "/run_as_role", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/role_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"metadata.scheduler_definition", "workflow.definition", "report.definition", "changeplan.reference_graph"}, WriteSet: []string{"metadata.definition_version", "changeplan.publication"}, Transaction: "reviewed_change_plan_transaction", Idempotency: "idempotency_key_and_plan_revision", SideEffects: []string{"audit:business_change_plan.item_applied", "schema_snapshot_rebuild"}, SideEffectLevel: "internal", Compensation: "append_new_version_restoring_previous_head", PermissionModel: "scheduler.definition.write", ChangeControl: "reviewed_system_draft_change_plan"},
		Errors:             schedulerDefinitionAuthoringErrors(), Examples: schedulerBusinessJobExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "model", Path: "runtime/infrastructure/persistence/database/appschema/definition_shape.go", Symbol: "metadataDefinitionShape"},
			{Kind: "validation", Path: "runtime/domain/scheduler/validation/scheduler_definition_validation.go", Symbol: "SchedulerValidateDefinitionContract"},
			{Kind: "service", Path: "runtime/application/scheduler/scheduler_application_service.go", Symbol: "SchedulerApplicationService.PreviewDefinition"},
		},
	}
}

func schedulerScheduleAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := schedulerScheduleParameters()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "scheduler.schedule", Status: "supported", Lifecycle: "definition_fragment", Permissions: []string{"scheduler.definition.read", "scheduler.definition.write"},
		Parameters: parameters, ValidationEndpoint: "POST /scheduler/schedules/preview", PreviewEndpoint: "POST /scheduler/schedules/preview", FrontendSupportKey: "scheduler.schedule.editor.v1",
		InputSchema: schedulerObjectSchema(parameters), OutputSchema: schedulerPreviewOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "next_runs", JSONPointer: "/next_runs", Type: "date_time_list", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"metadata.scheduler_definition"}, Transaction: "read_only_preview", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "scheduler.definition.write"},
		Errors:          schedulerDefinitionAuthoringErrors(), Examples: schedulerScheduleExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "validation", Path: "runtime/domain/scheduler/validation/scheduler_definition_validation.go", Symbol: "SchedulerValidateScheduleFragment"},
			{Kind: "transport", Path: "runtime/transport/http/scheduler/scheduler_routes.go", Symbol: "RegisterRoutes"},
			{Kind: "runtime", Path: "runtime/domain/scheduler/policy/scheduler_schedule_policy.go", Symbol: "SchedulerScheduleNextRunAt"},
		},
	}
}

func schedulerCommandAuthoringCapability(key, lifecycle, resourceParameter, route string, idempotencyRequired bool) capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: resourceParameter, Type: "record_id", Required: true}}
	if idempotencyRequired {
		parameters = append(parameters, capabilitycontract.CapabilityAuthoringParameter{Key: "idempotency_key", Type: "string", Required: true})
	}
	capability := capabilitycontract.CapabilityAuthoringDefinition{
		Key: key, Status: "supported", Lifecycle: lifecycle, Parameters: parameters, Requires: []string{"scheduler.business_job"}, Permissions: []string{"scheduler.command"},
		ConfigurationRoutes: []string{route}, FrontendSupportKey: "scheduler.command.v1", InputSchema: schedulerObjectSchema(parameters), OutputSchema: schedulerOperationOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "status", JSONPointer: "/status", Type: "string", VisibleTo: "subsequent_capability_calls"}, {Name: "run", JSONPointer: "/run", Type: "scheduler_run", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"metadata.scheduler_definition", "scheduler_service.schedule", "scheduler_service.run"}, WriteSet: []string{"scheduler_service.run", "scheduler_service.dead_letter"}, Transaction: "scheduler_owner_operation", Idempotency: "idempotency_key", SideEffects: []string{"scheduler_operation_audit"}, SideEffectLevel: "external", Compensation: "issue an explicit owner retry, cancel, resolve, or reschedule command; never roll back Scheduler evidence", PermissionModel: "scheduler.command"},
		Examples:        schedulerCommandExamples(resourceParameter, idempotencyRequired),
		Sources:         []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/application/scheduler/scheduler_operations.go", Symbol: schedulerCommandSymbol(key)}, {Kind: "transport", Path: "runtime/transport/http/scheduler/scheduler_routes.go", Symbol: "RegisterRoutes"}},
	}
	if key == "scheduler.job.simulate" {
		capability.SimulationEndpoint = route
		capability.Execution.WriteSet = nil
		capability.Execution.Idempotency = "naturally_idempotent"
		capability.Execution.SideEffectLevel = "none"
	}
	return capability
}

func schedulerResolveDeadLetterAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: "dead_letter_id", Type: "record_id", Required: true}, {Key: "idempotency_key", Type: "string", Required: true}, {Key: "note", Type: "string"}}
	capability := schedulerCommandAuthoringCapability("scheduler.dead_letter.resolve", "business_administrator_recovery", "dead_letter_id", "POST /scheduler/dead-letters/{deadLetterID}/resolve", true)
	capability.Parameters = parameters
	capability.InputSchema = schedulerObjectSchema(parameters)
	capability.Examples = []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"dead_letter_id": "deadletter_01", "idempotency_key": "resolve-deadletter-01"}},
		{Name: "representative", Value: map[string]any{"dead_letter_id": "deadletter_02", "idempotency_key": "resolve-deadletter-02", "note": "Failure cause corrected"}},
		{Name: "invalid_with_repair", Value: map[string]any{"dead_letter_id": "", "idempotency_key": ""}, ExpectedErrorCodes: []string{"backend.scheduler.dead_letter_not_found"}},
	}
	return capability
}

func schedulerCommandSymbol(key string) string {
	switch key {
	case "scheduler.job.simulate":
		return "SchedulerApplicationService.SimulateJob"
	case "scheduler.job.run":
		return "SchedulerApplicationService.RunJob"
	case "scheduler.run.retry":
		return "SchedulerApplicationService.RetryRun"
	case "scheduler.run.cancel":
		return "SchedulerApplicationService.CancelRun"
	default:
		return "SchedulerApplicationService.ResolveDeadLetter"
	}
}
