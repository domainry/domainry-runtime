package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

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
		InputSchema:  changePlanReferenceImpactInputSchema(),
		OutputSchema: changePlanReferenceImpactOutputSchema(), OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "reference_graph_hash", JSONPointer: "/graph_hash", Type: "reference_graph_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"changeplan.reference_graph"}, Transaction: "read_only_graph_projection", Idempotency: "naturally_idempotent_at_graph_hash", SideEffectLevel: "none", PermissionModel: "platform_admin.domain_impact.read"},
		Examples:  []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"resource_type": "object", "resource_key": "order"}}, {Name: "representative", Value: map[string]any{"resource_type": "field", "resource_key": "order.status"}}},
		Sources:   []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: "runtime/application/changeplan/change_plan_reference_application_service.go", Symbol: "ChangePlanReferenceApplicationService.Graph"}, {Kind: "domain", Path: "runtime/domain/changeplan/projection/changeplan_reference_graph_projection.go", Symbol: "ChangePlanReferenceImpact"}},
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

func changePlanSnapshotOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	properties := map[string]capabilitycontract.CapabilityAuthoringSchema{}
	for _, key := range []string{"runtime_metadata", "schema", "effective_permissions", "runtime_state", "identity_governance", "object_record_counts", "resource_visibility"} {
		properties[key] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	}
	objectItem := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	for _, key := range []string{"effective_menus", "resource_sources", "seed_records"} {
		properties[key] = capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &objectItem}
	}
	stringItem := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	properties["hidden_resource_categories"] = capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &stringItem}
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
