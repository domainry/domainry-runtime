package capability

import (
	"strings"
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestPublishedResourceOperationsAreExplicitAndBackedByRoutes(t *testing.T) {
	contract := RuntimeAuthoringCapabilities()
	directRequired := map[string]bool{
		"integration.connection": true, "seed.record": true,
	}
	systemDraftRequired := map[string]bool{
		"schema.object": true, "schema.field": true, "schema.relation": true, "schema.dictionary": true, "view.definition": true,
		"action.definition": true, "workflow.definition": true, "automation.rule": true, "scheduler.business_job": true,
		"principal.profile_binding": true, "report.definition": true, "integration.connector_definition": true,
		"maintenance.metadata_rollback": true,
	}
	declared := 0
	for _, domain := range contract.Domains {
		for _, capability := range domain.Capabilities {
			operations := capability.ResourceOperations
			if systemDraftRequired[capability.Key] {
				delete(systemDraftRequired, capability.Key)
				assertReviewedSystemDraftCapability(t, capability)
				continue
			}
			if directRequired[capability.Key] && operations == nil {
				t.Errorf("persistent authoring capability %s does not publish resource_operations", capability.Key)
			}
			if operations == nil {
				continue
			}
			delete(directRequired, capability.Key)
			declared++
			if operations.PersistenceMode != "versioned_resource" && operations.PersistenceMode != "mutable_resource" && operations.PersistenceMode != "immutable_resource" && operations.PersistenceMode != "audited_resource" {
				t.Errorf("%s has unsupported persistence mode %q", capability.Key, operations.PersistenceMode)
			}
			for name, endpoint := range map[string]string{"validate": operations.Validate, "upsert": operations.Upsert, "get": operations.Get} {
				if !validAuthoringOperationEndpoint(endpoint) {
					t.Errorf("%s resource operation %s must publish METHOD /path, got %q", capability.Key, name, endpoint)
				}
			}
			if !validAuthoringOperationEndpoint(operations.Versions) {
				t.Errorf("%s persistent operation versions must publish METHOD /path, got %q", capability.Key, operations.Versions)
			}
			assertDirectAuthoringUpsertHeaders(t, capability.Key, operations.UpsertHeaders)
			assertDirectAuthoringSuccessSchema(t, capability.Key, operations.SuccessSchema)
			if operations.PersistenceMode == "versioned_resource" {
				for name, endpoint := range map[string]string{"versions": operations.Versions} {
					if !validAuthoringOperationEndpoint(endpoint) {
						t.Errorf("%s versioned operation %s must publish METHOD /path, got %q", capability.Key, name, endpoint)
					}
				}
			}
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Validate)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Upsert)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Get)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Versions)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Simulate)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Rollback)
			assertAuthoringOperationBackedByPublishedRoute(t, capability, operations.Delete)
		}
	}
	if declared == 0 {
		t.Fatal("authoring catalog publishes no explicit resource operations")
	}
	for capabilityKey := range directRequired {
		t.Errorf("required persistent authoring capability %s is missing from the catalog", capabilityKey)
	}
	for capabilityKey := range systemDraftRequired {
		t.Errorf("required system-draft capability %s is missing from the catalog", capabilityKey)
	}
}

func assertReviewedSystemDraftCapability(t *testing.T, capability capabilitycontract.CapabilityAuthoringDefinition) {
	t.Helper()
	if capability.ResourceOperations != nil {
		t.Errorf("%s must not publish direct resource operations: %#v", capability.Key, capability.ResourceOperations)
	}
	for _, route := range capability.ConfigurationRoutes {
		if strings.HasPrefix(route, "PUT /metadata/definitions/") || strings.HasPrefix(route, "DELETE /metadata/definitions/") || strings.Contains(route, "/rollback") || strings.HasPrefix(route, "PUT /automation-rules/") || strings.HasPrefix(route, "DELETE /automation-rules/") || strings.HasPrefix(route, "PUT /identity/roles/") || strings.HasPrefix(route, "DELETE /identity/roles/") {
			t.Errorf("%s publishes direct definition mutation route %q", capability.Key, route)
		}
	}
	for _, required := range []string{"GET /domain-system-snapshot", "GET /domain-reference-graph", "PUT /tenant-admin/change-plans/{planID}", "POST /tenant-admin/change-plans/{planID}/review", "POST /tenant-admin/change-plans/{planID}/approve", "POST /tenant-admin/change-plans/apply"} {
		if !capabilityStringContains(capability.ConfigurationRoutes, required) {
			t.Errorf("%s is missing reviewed system draft route %q", capability.Key, required)
		}
	}
	if capability.Execution == nil || capability.Execution.ChangeControl != "reviewed_system_draft_change_plan" || capability.Execution.Transaction != "reviewed_change_plan_transaction" {
		t.Errorf("%s system-draft execution contract = %#v", capability.Key, capability.Execution)
	}
	if (capability.SystemDraftResourceType == "") == (capability.SystemDraftResourceTypeInputJSONPointer == "") {
		t.Errorf("%s must publish exactly one explicit system-draft resource type contract", capability.Key)
	}
}

func TestActionAndWorkflowDefinitionsPublishOnlyReviewedSystemDraftMutationRoutes(t *testing.T) {
	contract := RuntimeAuthoringCapabilities()
	found := map[string]bool{}
	for _, domain := range contract.Domains {
		for _, capability := range domain.Capabilities {
			if capability.Key != "action.definition" && capability.Key != "workflow.definition" {
				continue
			}
			found[capability.Key] = true
			if capability.ResourceOperations != nil {
				t.Fatalf("%s must not publish direct resource operations: %#v", capability.Key, capability.ResourceOperations)
			}
			for _, route := range capability.ConfigurationRoutes {
				if strings.HasPrefix(route, "PUT /metadata/definitions/") || strings.HasPrefix(route, "DELETE /metadata/definitions/") || (strings.HasPrefix(route, "POST /workflows/") && !strings.HasSuffix(route, "/validate")) {
					t.Fatalf("%s publishes direct mutation route %q", capability.Key, route)
				}
			}
			for _, required := range []string{"PUT /tenant-admin/change-plans/{planID}", "POST /tenant-admin/change-plans/{planID}/review", "POST /tenant-admin/change-plans/{planID}/approve", "POST /tenant-admin/change-plans/apply"} {
				if !capabilityStringContains(capability.ConfigurationRoutes, required) {
					t.Errorf("%s is missing reviewed system draft route %q", capability.Key, required)
				}
			}
			if capability.Execution == nil || capability.Execution.ChangeControl != "reviewed_system_draft_change_plan" || capability.Execution.Transaction != "reviewed_change_plan_transaction" {
				t.Fatalf("Action execution contract = %#v", capability.Execution)
			}
		}
	}
	for _, key := range []string{"action.definition", "workflow.definition"} {
		if !found[key] {
			t.Errorf("%s capability not found", key)
		}
	}
}

func assertDirectAuthoringSuccessSchema(t *testing.T, capabilityKey string, schema *capabilitycontract.CapabilityAuthoringSchema) {
	t.Helper()
	if schema == nil || schema.Type != "object" || schema.AdditionalProperties == nil || !*schema.AdditionalProperties {
		t.Errorf("%s has invalid success_schema root: %#v", capabilityKey, schema)
		return
	}
	want := map[string]string{"resource": "", "resource_hash": "string", "snapshot_hash": "string", "available_successors": "array"}
	for key, wantType := range want {
		property, found := schema.Properties[key]
		if !found || property.Type != wantType {
			t.Errorf("%s success_schema property %s=%#v", capabilityKey, key, property)
		}
		if !capabilityStringContains(schema.Required, key) {
			t.Errorf("%s success_schema does not require %s", capabilityKey, key)
		}
	}
	successors := schema.Properties["available_successors"]
	if successors.Items == nil || successors.Items.Type != "object" {
		t.Errorf("%s success_schema successor item=%#v", capabilityKey, successors.Items)
	}
}

func assertDirectAuthoringUpsertHeaders(t *testing.T, capabilityKey string, headers []capabilitycontract.CapabilityAuthoringRequestHeader) {
	t.Helper()
	want := map[string]string{
		"Builder-Task-ID":      "builder_task_id",
		"Idempotency-Key":      "request_fingerprint",
		"Expected-Schema-Hash": "expected_resource_hash",
	}
	if len(headers) != len(want) {
		t.Errorf("%s upsert_headers count=%d want=%d", capabilityKey, len(headers), len(want))
	}
	for _, header := range headers {
		valueSource, exists := want[header.Name]
		if !exists || !header.Required || header.ValueSource != valueSource || strings.TrimSpace(header.Description) == "" {
			t.Errorf("%s has invalid upsert header %#v", capabilityKey, header)
		}
		delete(want, header.Name)
	}
	for name := range want {
		t.Errorf("%s missing required upsert header %s", capabilityKey, name)
	}
}

func validAuthoringOperationEndpoint(endpoint string) bool {
	parts := strings.SplitN(strings.TrimSpace(endpoint), " ", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
		return false
	}
	switch parts[0] {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func assertAuthoringOperationBackedByPublishedRoute(t *testing.T, capability capabilitycontract.CapabilityAuthoringDefinition, endpoint string) {
	t.Helper()
	if endpoint == "" {
		return
	}
	if endpoint == capability.ValidationEndpoint || endpoint == capability.PreviewEndpoint || endpoint == capability.SimulationEndpoint {
		return
	}
	for _, route := range capability.ConfigurationRoutes {
		if route == endpoint {
			return
		}
	}
	// A capability may publish a resource-key-specific simulation while keeping
	// a body-selected compatibility endpoint in SimulationEndpoint.
	if strings.Contains(endpoint, "/simulate") && capability.SimulationEndpoint != "" {
		return
	}
	t.Errorf("%s resource operation %q is not backed by its published endpoints", capability.Key, endpoint)
}
