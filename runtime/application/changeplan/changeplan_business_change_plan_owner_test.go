package changeplan

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilityprojection "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"encoding/json"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	capabilitybusiness "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestValidateBusinessSystemChangePlanAcceptsReviewedIncrementalFacts(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Nodes: []ReferenceNode{}, Edges: []ReferenceEdge{}}
	plan := changePlanTestPlan(snapshot, graph)
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || !result.ApplyAllowed || len(result.Issues) != 0 || result.RiskSummary["low"] != 1 {
		t.Fatalf("unexpected valid plan result: %#v", result)
	}
	if len(result.Diffs) != 1 || result.Diffs[0].BusinessSummary != "Create domain field customer.segment" || len(result.Diffs[0].Changes) != 1 || result.Diffs[0].Changes[0].Path != "$" {
		t.Fatalf("expected normalized additive diff, got %#v", result.Diffs)
	}
}

func TestBusinessChangePlanDiffMarksPermissionExpansionAndReduction(t *testing.T) {
	base := BusinessSystemChangeItem{Operation: "update", ResourceType: "role_permission", ResourceKey: "sales", Before: json.RawMessage(`{"permissions":["customer.read"]}`), After: json.RawMessage(`{"permissions":["customer.read","customer.write"]}`)}
	diff := BuildBusinessChangeDiff(base, changePlanTestSnapshot(), ReferenceGraph{Hash: "graph"})
	if !changePlanSignalsContain(diff.RiskSignals, "permission_expansion") {
		t.Fatalf("expected permission expansion, got %#v", diff.RiskSignals)
	}
	base.Before, base.After = base.After, base.Before
	diff = BuildBusinessChangeDiff(base, changePlanTestSnapshot(), ReferenceGraph{Hash: "graph"})
	if !changePlanSignalsContain(diff.RiskSignals, "permission_reduction") {
		t.Fatalf("expected permission reduction, got %#v", diff.RiskSignals)
	}
}

func TestBusinessChangePlanValidatesObservedFrontendSupport(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	manifest := FrontendManifest{ManifestVersion: "frontend-capability-manifest-v1", FrontendVersion: "1", RuntimeContractVersions: []string{capabilitybusiness.RuntimeAuthoringContractVersion}, Entries: []FrontendSupportEntry{{SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.field"}, Route: "/system/metadata", RequiredPermissions: []string{"workspace.admin"}, FeatureModule: "metadata.tsx", AcceptanceTests: []string{"metadata.spec.ts"}}}}
	snapshot.FrontendCapabilities = FrontendCapabilities{Status: "complete", ManifestHash: "frontend-manifest-hash", Manifest: &manifest, MissingFrontendSupport: []FrontendRequirement{}, StaleFrontendSupport: []FrontendSupportEntry{}}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.FrontendManifestVersion, plan.FrontendManifestHash = manifest.ManifestVersion, snapshot.FrontendCapabilities.ManifestHash
	plan.Items[0].FrontendSupportKey = "metadata.field.editor.v1"
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !result.Valid {
		t.Fatalf("expected observed frontend support to validate: result=%#v err=%v", result, err)
	}
	plan.FrontendManifestHash = "stale"
	plan.Items[0].FrontendSupportKey = "metadata.future-editor.v1"
	result, err = ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"backend.change_plan.frontend_manifest_stale", "backend.change_plan.frontend_support_missing"} {
		if !changePlanIssuesContain(result.Issues, code) {
			t.Fatalf("missing %s in %#v", code, result.Issues)
		}
	}
}

func TestBusinessChangePlanRejectsStaleResourceVersion(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	snapshot.ResourceSources = []changeplanprojection.SystemResourceSource{{ResourceType: "field", ResourceKey: "customer.segment", SchemaHash: "field-current", SourceKind: "builder"}}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Items[0] = BusinessSystemChangeItem{ItemID: "update-segment", Operation: "update", ChangeKind: "compatible", RiskLevel: "low", ResourceType: "field", ResourceKey: "customer.segment", ResourceOwner: "builder", CapabilityKey: "schema.field", Before: json.RawMessage(`{"type":"text"}`), After: json.RawMessage(`{"type":"text","name":"Segment"}`), ValidationMethods: []string{"metadata.validate"}}
	plan.ReleaseOrder, plan.RollbackOrder = []string{"update-segment"}, []string{"update-segment"}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !changePlanIssuesContain(result.Issues, "backend.change_plan.resource_version_required") {
		t.Fatalf("expected required resource version: result=%#v err=%v", result, err)
	}
	plan.Items[0].ExpectedResourceHash = "field-stale"
	result, _ = ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if !changePlanIssuesContain(result.Issues, "backend.change_plan.resource_version_conflict") {
		t.Fatalf("expected resource version conflict: %#v", result)
	}
	plan.Items[0].ExpectedResourceHash = "field-current"
	result, err = ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !result.Valid {
		t.Fatalf("expected current resource version to validate: result=%#v err=%v", result, err)
	}
}

func TestBusinessChangePlanRejectsSnapshotWithHiddenCategories(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	snapshot.HiddenResourceCategories = []string{"identity_governance", "seed_records"}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !changePlanIssuesContain(result.Issues, "backend.change_plan.snapshot_incomplete") || result.ApplyAllowed {
		t.Fatalf("hidden snapshot must block apply: result=%#v err=%v", result, err)
	}
}

func TestValidateBusinessSystemChangePlanAggregatesStaleDestructiveAndOwnershipIssues(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{{FromType: "action", FromKey: "customer.qualify", ToType: "field", ToKey: "customer.status", Kind: "reads_field"}}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.SnapshotHash = "stale"
	plan.ReferenceGraphHash = "stale"
	plan.Reviewed = false
	plan.Items = []BusinessSystemChangeItem{{ItemID: "remove-status", Operation: "delete", ChangeKind: "destructive", RiskLevel: "critical", ResourceType: "field", ResourceKey: "customer.status", ResourceOwner: "unknown", CapabilityKey: "schema.unknown"}}
	plan.ReleaseOrder = []string{"missing"}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || result.ApplyAllowed || len(result.Issues) < 8 {
		t.Fatalf("expected aggregated invalid plan result: %#v", result)
	}
	for _, code := range []string{"backend.change_plan.snapshot_stale", "backend.change_plan.reference_graph_stale", "backend.change_plan.owner_unknown_protected", "backend.change_plan.capability_unknown", "backend.change_plan.before_required", "backend.change_plan.review_required", "backend.change_plan.replacement_required", "backend.change_plan.order_item_unknown"} {
		if !changePlanIssuesContain(result.Issues, code) {
			t.Fatalf("missing issue %s in %#v", code, result.Issues)
		}
	}
}

func TestValidateBusinessSystemChangePlanDerivesFieldTypeRiskInsteadOfTrustingLabels(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	snapshot.ObjectRecordCounts = map[string]int{"customer": 42}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Items = []BusinessSystemChangeItem{{ItemID: "change-segment-type", Operation: "update", ChangeKind: "compatible", RiskLevel: "low", ResourceType: "field", ResourceKey: "customer.segment", ResourceOwner: "builder", CapabilityKey: "schema.field", Before: json.RawMessage(`{"key":"segment","type":"text"}`), After: json.RawMessage(`{"key":"segment","type":"number"}`), ValidationMethods: []string{"metadata.validate"}}}
	plan.ReleaseOrder, plan.RollbackOrder = []string{"change-segment-type"}, []string{"change-segment-type"}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"backend.change_plan.change_kind_understated", "backend.change_plan.risk_understated", "backend.change_plan.review_required"} {
		if !changePlanIssuesContain(result.Issues, code) {
			t.Fatalf("missing derived risk issue %s in %#v", code, result.Issues)
		}
	}
	if result.Diffs[0].AffectedRecords != 42 || !changePlanSignalsContain(result.Diffs[0].RiskSignals, "existing_records_impact") {
		t.Fatalf("expected record-volume impact evidence, got %#v", result.Diffs[0])
	}
	plan.Items[0].ChangeKind, plan.Items[0].RiskLevel, plan.Reviewed, plan.ReviewedBy = "destructive", "high", true, "admin"
	result, err = ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !result.Valid || !result.ApplyAllowed {
		t.Fatalf("reviewed destructive field type plan should validate: result=%#v err=%v", result, err)
	}
	if len(result.Diffs) != 1 || !changePlanSignalsContain(result.Diffs[0].RiskSignals, "data_backfill_required") || !changePlanSignalsContain(result.Diffs[0].RiskSignals, "business_interruption") {
		t.Fatalf("expected derived field change risk signals, got %#v", result.Diffs)
	}
	if len(result.Diffs[0].Changes) != 1 || result.Diffs[0].Changes[0].Path != "type" || result.Diffs[0].Changes[0].Before != "text" || result.Diffs[0].Changes[0].After != "number" {
		t.Fatalf("expected field-level normalized diff, got %#v", result.Diffs[0].Changes)
	}
}

func TestBusinessChangePlanDiffIncludesRuntimeAndReferenceImpact(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	snapshot.RuntimeState.RunningWorkflowProcesses = []changeplanprojection.WorkflowProcessSummary{{ID: "process-1", WorkflowKey: "approval", Status: "running"}}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{
		{FromType: "scheduler_job", FromKey: "remind", ToType: "workflow", ToKey: "approval", Kind: "runs_workflow"},
		{FromType: "outbox_message", FromKey: "message-1", ToType: "workflow", ToKey: "approval", Kind: "waits_for_workflow"},
		{FromType: "frontend_surface", FromKey: "approval-admin", ToType: "workflow", ToKey: "approval", Kind: "configures_workflow"},
	}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Reviewed, plan.ReviewedBy = true, "admin"
	replacement := BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "approval-v2"}
	plan.Items = []BusinessSystemChangeItem{{ItemID: "archive-approval", Operation: "archive", ChangeKind: "destructive", RiskLevel: "high", ResourceType: "workflow", ResourceKey: "approval", ResourceOwner: "builder", CapabilityKey: "workflow.graph_v2", Before: json.RawMessage(`{"key":"approval"}`), Replacement: &replacement, ReferenceMigrations: []BusinessReferenceMigration{
		{Consumer: BusinessChangeTarget{ResourceType: "scheduler_job", ResourceKey: "remind"}, Replacement: replacement, Strategy: "manual", Reason: "Retarget the scheduled job during the reviewed maintenance window."},
		{Consumer: BusinessChangeTarget{ResourceType: "outbox_message", ResourceKey: "message-1"}, Replacement: replacement, Strategy: "preserve_in_flight", Reason: "The queued message keeps its immutable workflow reference."},
		{Consumer: BusinessChangeTarget{ResourceType: "frontend_surface", ResourceKey: "approval-admin"}, Replacement: replacement, Strategy: "manual", Reason: "Update the source-owned route binding in the same reviewed release."},
	}, ValidationMethods: []string{"workflow.validate"}}}
	plan.ReleaseOrder, plan.RollbackOrder = []string{"archive-approval"}, []string{"archive-approval"}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !result.Valid || len(result.Diffs) != 1 {
		t.Fatalf("expected valid impact-aware plan: result=%#v err=%v", result, err)
	}
	for _, signal := range []string{"business_interruption", "workflow_running_instances", "scheduler_execution_impact", "outbox_delivery_impact", "frontend_compatibility"} {
		if !changePlanSignalsContain(result.Diffs[0].RiskSignals, signal) {
			t.Fatalf("missing risk signal %s in %#v", signal, result.Diffs[0])
		}
	}
	if len(result.Diffs[0].ReferenceImpact.DirectConsumers) != 3 {
		t.Fatalf("expected complete direct impact evidence, got %#v", result.Diffs[0].ReferenceImpact)
	}
}

func TestBusinessChangePlanRequiresMigrationForEveryDirectConsumer(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash", Edges: []ReferenceEdge{{FromType: "automation", FromKey: "customer.sync", ToType: "workflow", ToKey: "approval", Kind: "starts_workflow"}}}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Reviewed, plan.ReviewedBy = true, "admin"
	replacement := BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "approval-v2"}
	plan.Items = []BusinessSystemChangeItem{{ItemID: "archive-approval", Operation: "archive", ChangeKind: "destructive", RiskLevel: "high", ResourceType: "workflow", ResourceKey: "approval", ResourceOwner: "builder", CapabilityKey: "workflow.graph_v2", Before: json.RawMessage(`{"key":"approval"}`), Replacement: &replacement, ValidationMethods: []string{"workflow.validate"}}}
	plan.ReleaseOrder, plan.RollbackOrder = []string{"archive-approval"}, []string{"archive-approval"}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !changePlanIssuesContain(result.Issues, "backend.change_plan.reference_migration_required") || result.ApplyAllowed {
		t.Fatalf("missing consumer migration must block plan: result=%#v err=%v", result, err)
	}
}

func TestBusinessChangePlanDiffMarksSeedDataImpact(t *testing.T) {
	item := BusinessSystemChangeItem{ItemID: "archive-customer", Operation: "archive", ChangeKind: "destructive", ResourceType: "object", ResourceKey: "customer"}
	graph := ReferenceGraph{Hash: "graph", Edges: []ReferenceEdge{{FromType: "seed_record", FromKey: "customer.customer_acme", ToType: "object", ToKey: "customer", Kind: "materialized_from_seed"}}}
	diff := BuildBusinessChangeDiff(item, changePlanTestSnapshot(), graph)
	if !changePlanSignalsContain(diff.RiskSignals, "seed_data_impact") || len(diff.ReferenceImpact.DirectConsumers) != 1 {
		t.Fatalf("expected seed data impact, got %#v", diff)
	}
}

func TestBusinessChangePlanPreflightRequiresInstalledConnectorAndReadyConnection(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	snapshot.RuntimeState.Connectors = []integrationmodel.ConnectorSchema{{Key: "crm", Source: "plugin:crm-connector", DefinitionReady: true, AdapterReady: true, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "sync"}}}}
	snapshot.RuntimeState.Connections = []changeplanprojection.IntegrationConnectionSummary{{Key: "crm_primary", ConnectorKey: "crm", Status: "configured", Ready: false}}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Items[0].Dependencies = []BusinessChangeTarget{{ResourceType: "plugin", ResourceKey: "crm-connector"}, {ResourceType: "connector", ResourceKey: "crm"}, {ResourceType: "connector_operation", ResourceKey: "crm.sync"}, {ResourceType: "connection", ResourceKey: "crm_primary"}}
	result, err := ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if !changePlanIssuesContain(result.Issues, "backend.integration.binding.connection_required") || result.ApplyAllowed {
		t.Fatalf("unready Connection must block preflight: %#v", result)
	}
	plan.Items[0].Dependencies[1].ResourceKey = "missing"
	result, err = ValidateBusinessSystemChangePlan(plan, snapshot, graph, changePlanAdmin())
	if err != nil || !changePlanIssuesContain(result.Issues, "backend.integration.connector.definition_not_ready") {
		t.Fatalf("missing target Connector must block preflight: result=%#v err=%v", result, err)
	}
}

func changePlanTestSnapshot() changeplanprojection.BusinessSystemSnapshot {
	contract := capabilityprojection.RuntimeAuthoringCapabilities()
	capabilityKeys := []string{}
	for _, domain := range contract.Domains {
		for _, capability := range domain.Capabilities {
			capabilityKeys = append(capabilityKeys, capability.Key)
		}
	}
	return changeplanprojection.BusinessSystemSnapshot{SnapshotVersion: changeplanprojection.BusinessSystemSnapshotVersion, SnapshotHash: "snapshot-hash", RuntimeVersion: capabilitybusiness.RuntimeCapabilityContractVersion, AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash, ResourceSources: []changeplanprojection.SystemResourceSource{}, CapabilityKeys: capabilityKeys}
}

func changePlanTestPlan(snapshot changeplanprojection.BusinessSystemSnapshot, graph ReferenceGraph) BusinessSystemChangePlan {
	return BusinessSystemChangePlan{
		PlanVersion: BusinessSystemChangePlanVersion, PlanID: "plan-1", BusinessReason: "Add customer segment", SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash,
		RuntimeVersion: snapshot.RuntimeVersion, AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		ReleaseOrder: []string{"add-segment"}, RollbackOrder: []string{"add-segment"}, Items: []BusinessSystemChangeItem{{
			ItemID: "add-segment", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "field", ResourceKey: "customer.segment", ResourceOwner: "builder", CapabilityKey: "schema.field", After: json.RawMessage(`{"key":"segment","type":"text"}`), ValidationMethods: []string{"metadata.validate"},
		}},
	}
}

func changePlanAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
}

func changePlanIssuesContain(issues []BusinessChangePlanValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func changePlanSignalsContain(signals []string, expected string) bool {
	for _, signal := range signals {
		if signal == expected {
			return true
		}
	}
	return false
}
