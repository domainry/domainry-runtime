package contract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func ChangePlanCurrentStateSnapshotAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.current_state_snapshot", Status: "supported", Lifecycle: "read_only_discovery",
		Permissions: []string{"workspace.admin"}, ConfigurationRoutes: []string{"GET /domain-system-snapshot"},
		InputSchema:  changePlanEmptyInputSchema(),
		OutputSchema: changePlanSnapshotOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "snapshot_hash", JSONPointer: "/snapshot_hash", Type: "snapshot_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"runtime.metadata", "metadata.schema_snapshot", "identity.governance", "workflow.definition", "integration.connector"}, Transaction: "read_only_snapshot", Idempotency: "naturally_idempotent_at_snapshot_hash", SideEffectLevel: "none", PermissionModel: "workspace.admin"},
		Examples:  []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{}}, {Name: "representative", Value: map[string]any{}}},
		Sources:   []capabilitycontract.CapabilityAuthoringSource{{Kind: "domain", Path: "runtime/domain/changeplan/projection/changeplan_system_snapshot.go", Symbol: "RuntimeNativeMetadataModel"}, {Kind: "service", Path: "runtime/application/businesssystem/business_system_application_service.go", Symbol: "BusinessSystemApplicationService.Snapshot"}},
	}
}

func ChangePlanReferenceImpactAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.reference_impact", Status: "supported", Lifecycle: "read_only_impact_analysis",
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "resource_type", Type: "string", Required: true}, {Key: "resource_key", Type: "string", Required: true}},
		Permissions: []string{"platform_admin.domain_impact.read"}, ConfigurationRoutes: []string{"GET /domain-reference-graph", "GET /domain-references/{resourceType}/{resourceKey}"},
		FrontendSupportKey: "maintenance.reference-impact.v1", MinimumFrontendVersion: "domainry-admin-0.1.0",
		InputSchema:  changePlanReferenceImpactInputSchema(),
		OutputSchema: changePlanReferenceImpactOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "reference_graph_hash", JSONPointer: "/graph_hash", Type: "reference_graph_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"changeplan.reference_graph"}, Transaction: "read_only_graph_projection", Idempotency: "naturally_idempotent_at_graph_hash", SideEffectLevel: "none", PermissionModel: "platform_admin.domain_impact.read"},
		Examples:  []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"resource_type": "object", "resource_key": "order"}}, {Name: "representative", Value: map[string]any{"resource_type": "field", "resource_key": "order.status"}}},
		Sources:   []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/application/changeplan/change_plan_reference_application_service.go", Symbol: "ChangePlanReferenceApplicationService.Graph"}, {Kind: "domain", Path: "runtime/domain/changeplan/projection/changeplan_reference_graph_projection.go", Symbol: "ChangePlanReferenceImpact"}},
	}
}

func ChangePlanValidationAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.change_plan_validation", Status: "supported", Lifecycle: "side_effect_free_preflight", Requires: []string{"maintenance.current_state_snapshot", "maintenance.reference_impact"},
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "plan_version", Type: "string", Required: true}, {Key: "plan_id", Type: "string", Required: true}, {Key: "business_reason", Type: "string", Required: true}, {Key: "snapshot_hash", Type: "string", Required: true}, {Key: "reference_graph_hash", Type: "string", Required: true}, {Key: "runtime_version", Type: "string", Required: true}, {Key: "authoring_contract_version", Type: "string", Required: true}, {Key: "authoring_contract_hash", Type: "string", Required: true}, {Key: "release_order", Type: "array", Required: true, ItemSchema: "change_item_id"}, {Key: "rollback_order", Type: "array", Required: true, ItemSchema: "change_item_id"}, {Key: "items", Type: "array", Required: true, ItemSchema: "business_system_change_item"}},
		Permissions: []string{"workspace.admin"}, ValidationEndpoint: "POST /tenant-admin/change-plans/validate", ConfigurationRoutes: []string{"GET /tenant-admin/change-plans/{planID}", "PUT /tenant-admin/change-plans/{planID}", "POST /tenant-admin/change-plans/{planID}/clone-current", "POST /tenant-admin/change-plans/{planID}/simulate", "POST /tenant-admin/change-plans/{planID}/review", "POST /tenant-admin/change-plans/{planID}/approve", "GET /tenant-admin/change-plans/{planID}/export", "POST /tenant-admin/change-plans/validate"},
		InputSchema:  changePlanInputSchema(),
		OutputSchema: changePlanValidationOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}, {Name: "apply_allowed", JSONPointer: "/apply_allowed", Type: "boolean", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"changeplan.snapshot", "changeplan.reference_graph", "changeplan.draft"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent_at_snapshot_and_graph_hash", SideEffectLevel: "none", PermissionModel: "workspace.admin"},
		Examples:  changePlanValidationExamples(),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.change_plan.snapshot_stale", FieldPath: "snapshot_hash", ParameterKeys: []string{"actual", "expected"}, MessageKey: "backend.change_plan.snapshot_stale"},
			{Code: "backend.change_plan.snapshot_incomplete", FieldPath: "snapshot_hash", ParameterKeys: []string{"hidden_categories"}, MessageKey: "backend.change_plan.snapshot_incomplete"},
			{Code: "backend.change_plan.reference_graph_stale", FieldPath: "reference_graph_hash", ParameterKeys: []string{"actual", "expected"}, MessageKey: "backend.change_plan.reference_graph_stale"},
			{Code: "backend.change_plan.review_required", FieldPath: "items[].operation", MessageKey: "backend.change_plan.review_required"},
			{Code: "backend.change_plan.review_not_ready", FieldPath: "plan", MessageKey: "backend.change_plan.review_not_ready"},
			{Code: "backend.change_plan.maker_checker_required", FieldPath: "plan", MessageKey: "backend.change_plan.maker_checker_required"},
			{Code: "backend.change_plan.resource_version_required", FieldPath: "items[].expected_resource_hash", MessageKey: "backend.change_plan.resource_version_required"},
			{Code: "backend.change_plan.resource_version_conflict", FieldPath: "items[].expected_resource_hash", ParameterKeys: []string{"actual", "expected"}, MessageKey: "backend.change_plan.resource_version_conflict"},
			{Code: "backend.change_plan.unique_admin_required", FieldPath: "items[].after", ParameterKeys: []string{"role"}, MessageKey: "backend.change_plan.unique_admin_required"},
			{Code: "backend.change_plan.reference_migration_required", FieldPath: "items[].reference_migrations", ParameterKeys: []string{"consumer_type", "consumer_key"}, MessageKey: "backend.change_plan.reference_migration_required"},
			{Code: "backend.change_plan.reference_migration_invalid", FieldPath: "items[].reference_migrations[]", ParameterKeys: []string{"consumer_type", "consumer_key"}, MessageKey: "backend.change_plan.reference_migration_invalid"},
			{Code: "backend.change_plan.reference_migration_order_invalid", FieldPath: "items[].reference_migrations[].change_item_id", ParameterKeys: []string{"change_item_id", "consumer_type", "consumer_key"}, MessageKey: "backend.change_plan.reference_migration_order_invalid"},
			{Code: "backend.change_plan.draft_version_conflict", FieldPath: "expected_revision", ParameterKeys: []string{"plan_id"}, MessageKey: "backend.change_plan.draft_version_conflict"},
			{Code: "backend.change_plan.draft_identity_mismatch", FieldPath: "plan.plan_id", MessageKey: "backend.change_plan.draft_identity_mismatch"},
			{Code: "backend.change_plan.draft_not_found", FieldPath: "plan_id", MessageKey: "backend.change_plan.draft_not_found"},
			{Code: "backend.change_plan.draft_invalid", FieldPath: "plan", MessageKey: "backend.change_plan.draft_invalid"},
			{Code: "backend.change_plan.scenario_key_required", FieldPath: "acceptance_scenarios[].key", MessageKey: "backend.change_plan.scenario_key_required"},
			{Code: "backend.change_plan.scenario_key_duplicate", FieldPath: "acceptance_scenarios[].key", MessageKey: "backend.change_plan.scenario_key_duplicate"},
			{Code: "backend.change_plan.scenario_kind_unsupported", FieldPath: "acceptance_scenarios[].kind", MessageKey: "backend.change_plan.scenario_kind_unsupported"},
			{Code: "backend.change_plan.scenario_resource_not_in_candidate", FieldPath: "acceptance_scenarios[].resource_key", MessageKey: "backend.change_plan.scenario_resource_not_in_candidate"},
			{Code: "backend.change_plan.acceptance_scenario_failed", FieldPath: "acceptance_scenarios", MessageKey: "backend.change_plan.acceptance_scenario_failed"},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "domain", Path: "runtime/domain/changeplan/model/changeplan_business_model.go", Symbol: "BusinessSystemChangePlan"}, {Kind: "validation", Path: "runtime/domain/changeplan/validation/changeplan_item_validation.go", Symbol: "validateItems"}, {Kind: "service", Path: "runtime/application/changeplan/changeplan_business_change_plan_draft_lifecycle_clone.go", Symbol: "SubmitDraftForReview"}, {Kind: "service", Path: "runtime/application/changeplan/change_plan_application_service.go", Symbol: "SimulateDraftScenarios"}},
	}
}

func ChangePlanApplyAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.change_plan_apply", Status: "supported", Lifecycle: "reviewed_transactional_metadata_apply", Requires: []string{"maintenance.change_plan_validation"},
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "plan_id", Type: "change_plan_id", Required: true}, {Key: "expected_revision", Type: "integer", Required: true}, {Key: "confirmation", Type: "string", Required: true}},
		Permissions: []string{"workspace.admin"}, ValidationEndpoint: "POST /tenant-admin/change-plans/validate", ConfigurationRoutes: []string{"POST /tenant-admin/change-plans/apply"}, AuditEvents: []string{"business_change_plan.item_applied"},
		InputSchema:  changePlanApplyInputSchema(),
		OutputSchema: changePlanApplyOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "plan_id", JSONPointer: "/result/plan_id", Type: "change_plan_id", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/result/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"changeplan.snapshot", "changeplan.reference_graph", "changeplan.draft"}, WriteSet: []string{"metadata.definition_version", "changeplan.publication", "changeplan.operation_receipt"}, Transaction: "reviewed_change_plan_transaction", Idempotency: "idempotency_key_and_plan_revision", SideEffects: []string{"audit:business_change_plan.item_applied", "schema_snapshot_rebuild"}, SideEffectLevel: "internal", Compensation: "versioned_metadata_rollback_or_compensating_change_plan", PermissionModel: "workspace.admin", ChangeControl: "reviewed_change_plan"},
		Examples:  changePlanApplyExamples(),
		Errors:    []capabilitycontract.CapabilityAuthoringError{{Code: "backend.change_plan.apply_not_allowed", FieldPath: "plan_id", MessageKey: "backend.change_plan.apply_not_allowed"}, {Code: "backend.change_plan.confirmation_invalid", FieldPath: "confirmation", MessageKey: "backend.change_plan.confirmation_invalid"}, {Code: "backend.change_plan.draft_version_conflict", FieldPath: "expected_revision", MessageKey: "backend.change_plan.draft_version_conflict"}, {Code: "backend.change_plan.apply_resource_unsupported", FieldPath: "plan.items[].resource_type", MessageKey: "backend.change_plan.apply_resource_unsupported"}},
		Sources:   []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/application/changeplan/changeplan_business_change_plan_draft_lifecycle_clone.go", Symbol: "PublishApprovedDraftIdempotent"}, {Kind: "storage", Path: "runtime/infrastructure/persistence/database/appschema/definition_context_store.go", Symbol: "ApplicationSchemaStore.ApplyDefinitionMutations"}},
	}
}

func ChangePlanRollbackPolicyAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.rollback_policy", Status: "supported", Lifecycle: "read_only_recovery_contract", Requires: []string{"maintenance.reference_impact"},
		Permissions: []string{"workspace.admin"}, ConfigurationRoutes: []string{"GET /domain-maintenance/rollback-policy"},
		InputSchema:  changePlanEmptyInputSchema(),
		OutputSchema: changePlanRollbackPolicyOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "rollback_policy_version", JSONPointer: "/version", Type: "contract_version", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"changeplan.rollback_policy"}, Transaction: "read_only_policy", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "workspace.admin"},
		Examples:  []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{}}, {Name: "representative", Value: map[string]any{}}},
		Sources:   []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/domain/changeplan/policy/changeplan_rollback_policy.go", Symbol: "ChangePlanBusinessMaintenanceRollbackPolicy"}},
	}
}

func changePlanEmptyInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}}
}

func changePlanReferenceImpactInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"resource_type", "resource_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resource_type": {Type: "string"}, "resource_key": {Type: "string"}}}
}

func changePlanInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	item := changePlanItemSchema()
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"plan_version", "plan_id", "business_reason", "snapshot_hash", "reference_graph_hash", "runtime_version", "authoring_contract_version", "authoring_contract_hash", "release_order", "rollback_order", "items"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"plan_version": {Type: "string", Const: changeplanmodel.BusinessSystemChangePlanVersion}, "plan_id": {Type: "string"}, "draft_revision": {Type: "integer"}, "business_reason": {Type: "string"}, "builder_task_id": {Type: "string"},
			"snapshot_hash": {Type: "string"}, "reference_graph_hash": {Type: "string"}, "runtime_version": {Type: "string"}, "authoring_contract_version": {Type: "string"}, "authoring_contract_hash": {Type: "string"},
			"frontend_manifest_version": {Type: "string"}, "frontend_manifest_hash": {Type: "string"},
			"release_order": changePlanStringArraySchema(), "rollback_order": changePlanStringArraySchema(), "non_automatic_rollback": changePlanStringArraySchema(),
			"acceptance_scenarios": {Type: "array", Items: changePlanAcceptanceScenarioSchema()},
			"items":                {Type: "array", MinItems: changePlanIntPointer(1), Items: &item},
		},
		Definitions: map[string]capabilitycontract.CapabilityAuthoringSchema{"business_system_change_item": item},
	}
}

func changePlanAcceptanceScenarioSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	expected := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"valid"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"valid": {Type: "boolean"}, "error_codes": changePlanStringArraySchema(),
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "kind", "resource_key", "expected"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "kind": {Type: "string", Const: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition}, "resource_key": {Type: "string"},
		"input": {Type: "object", AdditionalProperties: &open}, "record": {Type: "object", AdditionalProperties: &open}, "expected": expected,
	}}
}

func changePlanApplyInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"plan_id", "expected_revision", "confirmation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"plan_id": {Type: "string"}, "expected_revision": {Type: "integer", Minimum: changePlanFloatPointer(1)}, "confirmation": {Type: "string"}}}
}

func changePlanItemSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	target := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"resource_type", "resource_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resource_type": {Type: "string"}, "resource_key": {Type: "string"}, "reason": {Type: "string"}}}
	migration := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"consumer", "replacement", "strategy", "reason"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"consumer": target, "replacement": target, "strategy": {Type: "string"}, "change_item_id": {Type: "string"}, "reason": {Type: "string"}}}
	operationVariant := func(operation string, required ...string) capabilitycontract.CapabilityAuthoringSchema {
		return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open, Required: append([]string{"operation"}, required...), Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"operation": {Type: "string", Const: operation}}}
	}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"item_id", "operation", "change_kind", "risk_level", "resource_type", "resource_key", "resource_owner", "owner_authorized", "capability_key", "validation_methods"}, OneOf: []capabilitycontract.CapabilityAuthoringSchema{
		operationVariant("create", "after"), operationVariant("update", "before", "after"), operationVariant("archive", "before"), operationVariant("delete", "before"), operationVariant("noop"),
	}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"item_id": {Type: "string"}, "operation": {Type: "string", Enum: changePlanAnyEnum(changeplanmodel.BusinessChangeOperations())}, "change_kind": {Type: "string", Enum: changePlanAnyEnum(changeplanmodel.BusinessChangeKinds())}, "risk_level": {Type: "string", Enum: changePlanAnyEnum(changeplanmodel.BusinessChangeRiskLevels())},
		"resource_type": {Type: "string"}, "resource_key": {Type: "string"}, "resource_owner": {Type: "string", Enum: changePlanAnyEnum(changeplanmodel.BusinessResourceOwners())}, "expected_resource_hash": {Type: "string"}, "owner_authorized": {Type: "boolean"}, "capability_key": {Type: "string"},
		"before": {}, "after": {}, "dependencies": {Type: "array", Items: &target}, "impacts": {Type: "array", Items: &target}, "replacement": target, "reference_migrations": {Type: "array", Items: &migration},
		"validation_methods": changePlanStringArraySchema(), "rollback_method": {Type: "string"}, "frontend_support_key": {Type: "string"},
	}}
}

func changePlanStringArraySchema() capabilitycontract.CapabilityAuthoringSchema {
	return capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}}
}

func changePlanAnyEnum(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func changePlanIntPointer(value int) *int { return &value }

func changePlanFloatPointer(value float64) *float64 { return &value }

func changePlanSnapshotOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	properties := map[string]capabilitycontract.CapabilityAuthoringSchema{}
	for _, key := range []string{"runtime_metadata", "schema", "effective_permissions", "runtime_state", "identity_governance", "frontend_capabilities", "object_record_counts", "resource_visibility"} {
		properties[key] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	}
	for _, key := range []string{"effective_menus", "resource_sources", "seed_records"} {
		properties[key] = capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}
	}
	properties["hidden_resource_categories"] = changePlanStringArraySchema()
	for _, key := range []string{"snapshot_version", "snapshot_hash", "runtime_version", "authoring_contract_version", "authoring_contract_hash", "schema_hash"} {
		properties[key] = capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"snapshot_version", "snapshot_hash", "runtime_metadata", "runtime_version", "authoring_contract_version", "authoring_contract_hash", "schema_hash", "schema"}, Properties: properties}
}

func changePlanReferenceImpactOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	edge := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"from_type", "from_key", "to_type", "to_key", "kind"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"from_type": {Type: "string"}, "from_key": {Type: "string"}, "to_type": {Type: "string"}, "to_key": {Type: "string"}, "kind": {Type: "string"}, "path": {Type: "string"}}}
	edges := capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &edge}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"resource_type", "resource_key", "graph_hash", "direct_consumers", "direct_dependencies", "indirect_consumers", "deletion_blocked"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resource_type": {Type: "string"}, "resource_key": {Type: "string"}, "graph_hash": {Type: "string"}, "direct_consumers": edges, "direct_dependencies": edges, "indirect_consumers": edges, "deletion_blocked": {Type: "boolean"}}}
}

func changePlanValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "apply_allowed", "current_snapshot_hash", "current_reference_graph_hash", "risk_summary", "diffs", "issues"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"valid": {Type: "boolean"}, "apply_allowed": {Type: "boolean"}, "current_snapshot_hash": {Type: "string"}, "current_reference_graph_hash": {Type: "string"}, "risk_summary": {Type: "object", AdditionalProperties: &open}, "diffs": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}, "issues": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}}}
}

func changePlanApplyOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	validation := *changePlanValidationOutputSchema()
	validation.Schema = ""
	result := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"plan_id", "status", "applied_definitions", "schema_hash", "validation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"plan_id": {Type: "string"}, "status": {Type: "string", Const: "applied"}, "applied_definitions": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}, "schema_hash": {Type: "string"}, "validation": validation}}
	snapshot := *changePlanSnapshotOutputSchema()
	snapshot.Schema = ""
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"result", "current_snapshot"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"result": result, "current_snapshot": snapshot}}
}

func changePlanRollbackPolicyOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	resource := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"resource_types", "strategy", "preserves_run_evidence", "notes"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resource_types": changePlanStringArraySchema(), "strategy": {Type: "string", Enum: changePlanAnyEnum(changeplanmodel.BusinessRollbackStrategies())}, "preserves_run_evidence": {Type: "boolean"}, "safety_checks": changePlanStringArraySchema(), "notes": {Type: "string"}}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"version", "resources"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"version": {Type: "string"}, "resources": {Type: "array", Items: &resource}}}
}

func changePlanValidationExamples() []capabilitycontract.CapabilityAuthoringExample {
	plan := map[string]any{"plan_version": changeplanmodel.BusinessSystemChangePlanVersion, "plan_id": "plan_order_status", "business_reason": "Add order status", "snapshot_hash": "$instance.snapshot_hash", "reference_graph_hash": "$instance.reference_graph_hash", "runtime_version": "$runtime.version", "authoring_contract_version": "$contract.version", "authoring_contract_hash": "$contract.hash", "release_order": []any{"create-field"}, "rollback_order": []any{"create-field"}, "items": []any{map[string]any{"item_id": "create-field", "operation": "create", "change_kind": "additive", "risk_level": "low", "resource_type": "field", "resource_key": "order.status", "resource_owner": "builder", "owner_authorized": true, "capability_key": "schema.field", "after": map[string]any{"key": "status", "type": "text"}, "validation_methods": []any{"metadata_field_validator"}}}}
	representative := changePlanCopyExample(plan)
	representative["builder_task_id"] = "$builder_task_id"
	invalid := changePlanCopyExample(plan)
	invalid["snapshot_hash"] = "stale"
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: plan}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: invalid, ExpectedErrorCodes: []string{"backend.change_plan.snapshot_stale"}}}
}

func changePlanApplyExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"plan_id": "plan_order_status", "expected_revision": 3, "confirmation": "plan_order_status"}},
		{Name: "representative", Value: map[string]any{"plan_id": "plan_order_status", "expected_revision": 3, "confirmation": "plan_order_status"}},
		{Name: "invalid_with_repair", Value: map[string]any{"plan_id": "plan_order_status", "expected_revision": 3, "confirmation": "wrong-plan"}, ExpectedErrorCodes: []string{"backend.change_plan.confirmation_invalid"}},
	}
}

func changePlanCopyExample(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
