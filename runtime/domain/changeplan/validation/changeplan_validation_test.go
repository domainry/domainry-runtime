package validation

import (
	"encoding/json"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestValidateBusinessSystemChangePlanValidAndIdentityFailures(t *testing.T) {
	plan, snapshot, graph := validChangePlanFixture()
	result := ValidateBusinessSystemChangePlan(plan, snapshot, graph)
	if !result.Valid || !result.ApplyAllowed || len(result.Issues) != 0 || result.RiskSummary["low"] != 1 {
		t.Fatalf("valid result = %#v", result)
	}

	invalid := plan
	invalid.PlanVersion = "old"
	invalid.PlanID = " "
	invalid.BusinessReason = ""
	invalid.SnapshotHash = "stale"
	invalid.ReferenceGraphHash = "stale"
	invalid.RuntimeVersion = "old"
	invalid.AuthoringContractVersion = "old"
	invalid.AuthoringContractHash = "old"
	invalid.FrontendManifestVersion = "old"
	invalid.FrontendManifestHash = "old"
	invalid.Items = nil
	invalid.ReleaseOrder = nil
	invalid.RollbackOrder = nil
	invalid.Reviewed = true
	invalid.ReviewedBy = ""
	snapshot.HiddenResourceCategories = []string{"secret"}
	result = ValidateBusinessSystemChangePlan(invalid, snapshot, graph)
	for _, code := range []string{
		"backend.change_plan.version_invalid", "backend.change_plan.plan_id_required", "backend.change_plan.business_reason_required",
		"backend.change_plan.snapshot_stale", "backend.change_plan.reference_graph_stale", "backend.change_plan.runtime_version_mismatch",
		"backend.change_plan.authoring_contract_mismatch", "backend.change_plan.frontend_manifest_stale",
		"backend.change_plan.items_required", "backend.change_plan.snapshot_incomplete",
	} {
		assertChangePlanIssue(t, result, code)
	}
}

func TestChangePlanItemValidationFailuresAndOrders(t *testing.T) {
	plan, snapshot, graph := validChangePlanFixture()
	plan.Reviewed = false
	plan.Items = []changeplanmodel.BusinessSystemChangeItem{
		{ItemID: "same", Operation: "update", ChangeKind: "additive", RiskLevel: "low", ResourceType: "role", ResourceKey: "", ResourceOwner: "unknown", CapabilityKey: "missing"},
		{ItemID: "same", Operation: "bogus", ChangeKind: "bogus", RiskLevel: "bogus", ResourceType: "", ResourceOwner: "plugin", CapabilityKey: "missing"},
		{ItemID: "", Operation: "create", ChangeKind: "compatible", RiskLevel: "low", ResourceType: "field", ResourceKey: "x", ResourceOwner: "manual", CapabilityKey: "cap", After: json.RawMessage(`null`)},
	}
	plan.ReleaseOrder = []string{"same", "same", "unknown"}
	plan.RollbackOrder = nil
	result := ValidateBusinessSystemChangePlan(plan, snapshot, graph)
	for _, code := range []string{
		"backend.change_plan.item_id_invalid", "backend.change_plan.operation_invalid", "backend.change_plan.change_kind_invalid",
		"backend.change_plan.change_kind_understated", "backend.change_plan.risk_level_invalid", "backend.change_plan.risk_understated",
		"backend.change_plan.resource_type_required", "backend.change_plan.resource_key_required", "backend.change_plan.owner_unknown_protected",
		"backend.change_plan.plugin_lifecycle_required", "backend.change_plan.owner_authorization_required", "backend.change_plan.capability_unknown",
		"backend.change_plan.before_required", "backend.change_plan.after_required", "backend.change_plan.validation_required",
		"backend.change_plan.review_required", "backend.change_plan.order_item_unknown", "backend.change_plan.order_item_duplicate",
		"backend.change_plan.order_item_missing",
	} {
		assertChangePlanIssue(t, result, code)
	}
}

func TestChangePlanResourceVersionOwnerAndFrontendCompatibility(t *testing.T) {
	plan, snapshot, graph := validChangePlanFixture()
	snapshot.ResourceSources = []changeplanmodel.ResourceSource{{ResourceType: "field", ResourceKey: "customer.name", SchemaHash: "v2", SourceKind: "template"}}
	item := plan.Items[0]
	item.Operation = "update"
	item.ChangeKind = "compatible"
	item.Before = json.RawMessage(`{"type":"text"}`)
	item.After = json.RawMessage(`{"type":"text"}`)
	item.ResourceOwner = "manual"
	item.OwnerAuthorized = true
	item.ExpectedResourceHash = ""
	item.FrontendSupportKey = "customer.edit"
	plan.Items = []changeplanmodel.BusinessSystemChangeItem{item}
	result := ValidateBusinessSystemChangePlan(plan, snapshot, graph)
	assertChangePlanIssue(t, result, "backend.change_plan.resource_version_required")
	assertChangePlanIssue(t, result, "backend.change_plan.owner_mismatch")
	assertChangePlanIssue(t, result, "backend.change_plan.frontend_support_missing")

	item.ExpectedResourceHash = "wrong"
	plan.Items[0] = item
	assertChangePlanIssue(t, ValidateBusinessSystemChangePlan(plan, snapshot, graph), "backend.change_plan.resource_version_conflict")

	snapshot.FrontendCapabilities.Manifest = nil
	assertChangePlanIssue(t, ValidateBusinessSystemChangePlan(plan, snapshot, graph), "backend.change_plan.frontend_manifest_unknown")

	snapshot.FrontendCapabilities.Manifest = &changeplanmodel.FrontendManifest{ManifestVersion: "front-v1", Entries: []changeplanmodel.FrontendSupportEntry{{SupportKey: "other"}, {SupportKey: "customer.edit", CapabilityKeys: []string{"other", "cap"}}}}
	snapshot.ResourceSources[0].SourceKind = "manual"
	item.ExpectedResourceHash = "v2"
	plan.Items[0] = item
	result = ValidateBusinessSystemChangePlan(plan, snapshot, graph)
	if len(result.Issues) != 0 {
		t.Fatalf("matching owner/version/frontend = %#v", result.Issues)
	}
}

func TestChangePlanRuntimeDependencyReadiness(t *testing.T) {
	_, snapshot, _ := validChangePlanFixture()
	snapshot.RuntimeState.Connectors = []integrationmodel.ConnectorSchema{
		{Key: "bad-definition"},
		{Key: "bad-adapter", DefinitionReady: true},
		{Key: "ready", Source: "plugin:mail", DefinitionReady: true, AdapterReady: true, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}},
	}
	snapshot.RuntimeState.Connections = []changeplanmodel.IntegrationConnection{{Key: "off", Ready: false}, {Key: "on", Ready: true}}
	validator := businessChangePlanValidator{snapshot: snapshot, result: changeplanmodel.BusinessChangePlanValidation{}}
	item := changeplanmodel.BusinessSystemChangeItem{ItemID: "item", Dependencies: []changeplanmodel.BusinessChangeTarget{
		{ResourceType: "plugin", ResourceKey: "missing"}, {ResourceType: "plugin", ResourceKey: "plugin:mail"},
		{ResourceType: "connector", ResourceKey: "missing"}, {ResourceType: "connector", ResourceKey: "bad-definition"}, {ResourceType: "connector", ResourceKey: "bad-adapter"}, {ResourceType: "connector", ResourceKey: "ready"},
		{ResourceType: "connector_operation", ResourceKey: "invalid"}, {ResourceType: "connector_operation", ResourceKey: "missing.send"}, {ResourceType: "connector_operation", ResourceKey: "bad-adapter.send"}, {ResourceType: "connector_operation", ResourceKey: "ready.missing"}, {ResourceType: "connector_operation", ResourceKey: "ready.send"},
		{ResourceType: "connection", ResourceKey: "missing"}, {ResourceType: "connection", ResourceKey: "off"}, {ResourceType: "connection", ResourceKey: "on"},
	}}
	validator.validateRuntimeDependencies(0, item)
	if len(validator.result.Issues) != 10 {
		t.Fatalf("dependency issues = %#v", validator.result.Issues)
	}
	if connector, ok := validator.connector("ready"); !ok || !connector.DefinitionReady || !connector.AdapterReady || connector.Source != "plugin:mail" {
		t.Fatalf("connector = %#v/%v", connector, ok)
	}
}

func TestChangePlanReferenceMigrationContracts(t *testing.T) {
	edge := changeplanmodel.ReferenceEdge{FromType: "workflow", FromKey: "consumer", ToType: "role", ToKey: "admin"}
	replacement := changeplanmodel.BusinessChangeTarget{ResourceType: "role", ResourceKey: "replacement"}
	deletion := changeplanmodel.BusinessSystemChangeItem{ItemID: "delete", Operation: "delete", ResourceType: "role", ResourceKey: "admin", Replacement: &replacement}
	update := changeplanmodel.BusinessSystemChangeItem{ItemID: "update", Operation: "update", ResourceType: "workflow", ResourceKey: "consumer"}
	newValidator := func() *businessChangePlanValidator {
		return &businessChangePlanValidator{plan: changeplanmodel.BusinessSystemChangePlan{Items: []changeplanmodel.BusinessSystemChangeItem{update, deletion}, ReleaseOrder: []string{"update", "delete"}}, result: changeplanmodel.BusinessChangePlanValidation{}}
	}

	validator := newValidator()
	validator.validateReferenceMigrations(1, deletion, []changeplanmodel.ReferenceEdge{edge})
	assertValidatorIssue(t, validator, "backend.change_plan.reference_migration_required")

	cases := []struct {
		name      string
		migration changeplanmodel.BusinessReferenceMigration
		reviewed  bool
		code      string
	}{
		{"invalid shape", changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "manual"}, false, "backend.change_plan.reference_migration_invalid"},
		{"update missing item", changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "update_reference", ChangeItemID: "", Reason: "move"}, false, "backend.change_plan.reference_migration_order_invalid"},
		{"preserve invalid type", changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "preserve_in_flight", Reason: "move"}, false, "backend.change_plan.reference_migration_invalid"},
		{"manual review", changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "manual", Reason: "move"}, false, "backend.change_plan.review_required"},
		{"unknown strategy", changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "unknown", Reason: "move"}, false, "backend.change_plan.reference_migration_invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			validator := newValidator()
			validator.plan.Reviewed = test.reviewed
			item := deletion
			item.ReferenceMigrations = []changeplanmodel.BusinessReferenceMigration{test.migration}
			validator.validateReferenceMigrations(1, item, []changeplanmodel.ReferenceEdge{edge})
			assertValidatorIssue(t, validator, test.code)
		})
	}

	validMigration := changeplanmodel.BusinessReferenceMigration{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: replacement, Strategy: "update_reference", ChangeItemID: "update", Reason: "move"}
	validator = newValidator()
	deletion.ReferenceMigrations = []changeplanmodel.BusinessReferenceMigration{validMigration}
	validator.validateReferenceMigrations(1, deletion, []changeplanmodel.ReferenceEdge{edge})
	if len(validator.result.Issues) != 0 {
		t.Fatalf("valid migration = %#v", validator.result.Issues)
	}

	runtimeEdge := edge
	runtimeEdge.FromType = "workflow_process"
	runtimeEdge.FromKey = "running"
	deletion.ReferenceMigrations = []changeplanmodel.BusinessReferenceMigration{{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow_process", ResourceKey: "running"}, Replacement: replacement, Strategy: "preserve_in_flight", Reason: "finish"}}
	validator.validateReferenceMigrations(1, deletion, []changeplanmodel.ReferenceEdge{runtimeEdge})

	manual := validMigration
	manual.Strategy, manual.ChangeItemID = "manual", ""
	deletion.ReferenceMigrations = []changeplanmodel.BusinessReferenceMigration{manual}
	validator = newValidator()
	validator.plan.Reviewed = true
	validator.validateReferenceMigrations(1, deletion, []changeplanmodel.ReferenceEdge{edge})

	validator = newValidator()
	if validator.migrationChangeItemPrecedes("", "delete", edge) || validator.migrationChangeItemPrecedes("missing", "delete", edge) {
		t.Fatal("invalid migration item preceded deletion")
	}
	validator.plan.ReleaseOrder = []string{"delete", "update"}
	if validator.migrationChangeItemPrecedes("update", "delete", edge) {
		t.Fatal("late migration preceded deletion")
	}
	validator.plan.ReleaseOrder = []string{"update"}
	if validator.migrationChangeItemPrecedes("update", "delete", edge) {
		t.Fatal("missing deletion order preceded deletion")
	}
}

func TestChangePlanReferenceImpactAndRemainingValidationEdges(t *testing.T) {
	plan, snapshot, graph := validChangePlanFixture()
	validator := businessChangePlanValidator{plan: plan, snapshot: snapshot, graph: graph, result: changeplanmodel.BusinessChangePlanValidation{RiskSummary: map[string]int{}}}
	validator.validateReferenceImpact(0, plan.Items[0])
	deletion := plan.Items[0]
	deletion.Operation, deletion.ChangeKind = "delete", "destructive"
	validator.validateReferenceImpact(0, deletion)
	graph.Edges = []changeplanmodel.ReferenceEdge{{FromType: "workflow", FromKey: "flow", ToType: "field", ToKey: deletion.ResourceKey}}
	validator.graph = graph
	validator.validateReferenceImpact(0, deletion)
	assertValidatorIssue(t, &validator, "backend.change_plan.replacement_required")
	deletion.Replacement = &changeplanmodel.BusinessChangeTarget{ResourceType: "field", ResourceKey: "new"}
	validator.validateReferenceImpact(0, deletion)
	assertValidatorIssue(t, &validator, "backend.change_plan.reference_migration_required")

	validator.result.Issues = nil
	validator.validateOwner(0, changeplanmodel.BusinessSystemChangeItem{ResourceOwner: "invalid"})
	assertValidatorIssue(t, &validator, "backend.change_plan.owner_invalid")
	validator.validateFrontendCompatibility(0, changeplanmodel.BusinessSystemChangeItem{})

	snapshot.ResourceSources = []changeplanmodel.ResourceSource{{ResourceType: "other", ResourceKey: "x", SchemaHash: "hash"}, {ResourceType: "field", ResourceKey: "customer.name"}}
	validator.snapshot = snapshot
	validator.validateExpectedResourceVersion(0, changeplanmodel.BusinessSystemChangeItem{Operation: "update", ResourceType: "field", ResourceKey: "customer.name"})

	plan.Items[0].RiskLevel = "high"
	plan.Reviewed, plan.ReviewedBy = true, ""
	assertChangePlanIssue(t, ValidateBusinessSystemChangePlan(plan, snapshot, graph), "backend.change_plan.reviewer_required")
}

func TestChangePlanSupportHelpers(t *testing.T) {
	if businessReferenceResourceType(" automation_rule ") != "automation" || !rawJSONPresent(json.RawMessage(`{}`)) || rawJSONPresent(nil) || rawJSONPresent(json.RawMessage(`null`)) {
		t.Fatal("normalization helpers")
	}
	flat := flattenBusinessChangeStrings(businessChangeJSONValue(json.RawMessage(`{"a":["one",{"b":"two"}],"n":3}`)))
	if !flat["one"] || !flat["two"] || len(flat) != 2 {
		t.Fatalf("flattened = %#v", flat)
	}
	if changePlanValueAllowed("x", "a", "x") != true || changePlanValueAllowed("x", "a") || changePlanIndexPath(2, "field") != "items[2].field" {
		t.Fatal("value/path helpers")
	}
	if !runtimeEvidenceResourceType("workflow_process") || !runtimeEvidenceResourceType("scheduler_run") || !runtimeEvidenceResourceType("scheduler_dead_letter") || !runtimeEvidenceResourceType("outbox_message") || runtimeEvidenceResourceType("field") {
		t.Fatal("runtime evidence types")
	}
	if len(RuntimeBusinessChangeOperations()) != 5 || len(RuntimeBusinessChangeKinds()) != 4 || len(RuntimeBusinessChangeRiskLevels()) != 4 || len(RuntimeBusinessResourceOwners()) != 6 {
		t.Fatal("published enums")
	}
}

func TestChangePlanShortCircuitConditionOutcomes(t *testing.T) {
	plan, snapshot, graph := validChangePlanFixture()
	validator := businessChangePlanValidator{plan: plan, snapshot: snapshot, graph: graph, result: changeplanmodel.BusinessChangePlanValidation{RiskSummary: map[string]int{}}}

	base := plan.Items[0]
	base.Operation, base.ChangeKind = "update", "compatible"
	base.Before, base.After = json.RawMessage(`{"type":"text"}`), json.RawMessage(`{"type":"text"}`)
	for _, risk := range []string{"high", "critical"} {
		item := base
		item.ResourceType, item.RiskLevel = "role", risk
		validator.plan.Items = []changeplanmodel.BusinessSystemChangeItem{item}
		validator.plan.ReleaseOrder, validator.plan.RollbackOrder = []string{item.ItemID}, []string{item.ItemID}
		validator.validateItems()
	}
	for _, operation := range []string{"archive", "noop"} {
		item := base
		item.Operation = operation
		item.ChangeKind = map[string]string{"archive": "destructive", "noop": "compatible"}[operation]
		item.ValidationMethods = nil
		validator.plan.Items = []changeplanmodel.BusinessSystemChangeItem{item}
		validator.plan.ReleaseOrder, validator.plan.RollbackOrder = []string{item.ItemID}, []string{item.ItemID}
		validator.validateItems()
	}

	snapshot.ResourceSources = []changeplanmodel.ResourceSource{
		{ResourceType: "field", ResourceKey: "different", SchemaHash: "hash", SourceKind: "manual"},
		{ResourceType: "field", ResourceKey: base.ResourceKey, SchemaHash: "hash", SourceKind: "unknown"},
	}
	validator.snapshot = snapshot
	validator.validateExpectedResourceVersion(0, changeplanmodel.BusinessSystemChangeItem{Operation: "archive", ResourceType: "field", ResourceKey: base.ResourceKey, ExpectedResourceHash: "hash"})
	validator.validateOwner(0, changeplanmodel.BusinessSystemChangeItem{Operation: "noop", ResourceType: "field", ResourceKey: base.ResourceKey, ResourceOwner: "plugin"})
	validator.validateOwner(0, changeplanmodel.BusinessSystemChangeItem{Operation: "noop", ResourceType: "field", ResourceKey: base.ResourceKey, ResourceOwner: "manual"})
	validator.validateReferenceImpact(0, changeplanmodel.BusinessSystemChangeItem{Operation: "archive", ResourceType: "field", ResourceKey: base.ResourceKey})
	graph.Edges = []changeplanmodel.ReferenceEdge{{FromType: "workflow", FromKey: "consumer", ToType: "field", ToKey: base.ResourceKey}}
	validator.graph = graph
	validator.validateReferenceImpact(0, changeplanmodel.BusinessSystemChangeItem{Operation: "delete", ResourceType: "field", ResourceKey: base.ResourceKey, Replacement: &changeplanmodel.BusinessChangeTarget{}})

	edge := graph.Edges[0]
	replacement := changeplanmodel.BusinessChangeTarget{ResourceType: "field", ResourceKey: "new"}
	deletion := changeplanmodel.BusinessSystemChangeItem{ItemID: "delete", Replacement: &replacement}
	for _, migrationReplacement := range []changeplanmodel.BusinessChangeTarget{{ResourceType: "other", ResourceKey: "new"}, {ResourceType: "field", ResourceKey: "other"}} {
		deletion.ReferenceMigrations = []changeplanmodel.BusinessReferenceMigration{{Consumer: changeplanmodel.BusinessChangeTarget{ResourceType: "workflow", ResourceKey: "consumer"}, Replacement: migrationReplacement, Reason: "reason"}}
		validator.validateReferenceMigrations(0, deletion, []changeplanmodel.ReferenceEdge{edge})
	}
	for _, candidate := range []changeplanmodel.BusinessSystemChangeItem{
		{ItemID: "candidate", ResourceType: "other", ResourceKey: "consumer", Operation: "update"},
		{ItemID: "candidate", ResourceType: "workflow", ResourceKey: "other", Operation: "update"},
		{ItemID: "candidate", ResourceType: "workflow", ResourceKey: "consumer", Operation: "create"},
	} {
		validator.plan.Items = []changeplanmodel.BusinessSystemChangeItem{candidate}
		if validator.migrationChangeItemPrecedes("candidate", "delete", edge) {
			t.Fatal("invalid candidate preceded deletion")
		}
	}

	validator.snapshot.RuntimeState.Connectors = []integrationmodel.ConnectorSchema{
		{Key: "definition-off", Source: "plugin:def", DefinitionReady: false, AdapterReady: true},
		{Key: "adapter-off", Source: "plugin:adapter", DefinitionReady: true, AdapterReady: false},
	}
	if validator.pluginReady("def") || validator.pluginReady("adapter") || validator.connectorOperationReady("definition-off.run") {
		t.Fatal("unready connector reported ready")
	}

	identityValidator := businessChangePlanValidator{plan: plan, snapshot: snapshot, graph: graph, result: changeplanmodel.BusinessChangePlanValidation{}}
	identityValidator.plan.AuthoringContractHash = "different"
	identityValidator.plan.FrontendManifestHash = "different"
	identityValidator.validateIdentityAndVersions()
	warningValidator := businessChangePlanValidator{result: changeplanmodel.BusinessChangePlanValidation{Issues: []changeplanmodel.BusinessChangePlanValidationIssue{{Severity: "warning"}}}}
	if warningValidator.hasErrors() {
		t.Fatal("warning treated as error")
	}
}

func validChangePlanFixture() (changeplanmodel.BusinessSystemChangePlan, changeplanmodel.Snapshot, changeplanmodel.ReferenceGraph) {
	snapshot := changeplanmodel.Snapshot{
		SnapshotHash: "snapshot-v1", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "authoring-v1", AuthoringContractHash: "authoring-hash", CapabilityKeys: []string{"cap"},
		FrontendCapabilities: changeplanmodel.FrontendCapabilities{ManifestHash: "front-hash", Manifest: &changeplanmodel.FrontendManifest{ManifestVersion: "front-v1"}},
	}
	graph := changeplanmodel.ReferenceGraph{Hash: "graph-v1"}
	item := changeplanmodel.BusinessSystemChangeItem{ItemID: "create-field", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "field", ResourceKey: "customer.name", ResourceOwner: "builder", CapabilityKey: "cap", After: json.RawMessage(`{"type":"text"}`), ValidationMethods: []string{"validate"}}
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "plan", BusinessReason: "reason", SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash,
		RuntimeVersion: snapshot.RuntimeVersion, AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		FrontendManifestVersion: "front-v1", FrontendManifestHash: "front-hash", Items: []changeplanmodel.BusinessSystemChangeItem{item}, ReleaseOrder: []string{item.ItemID}, RollbackOrder: []string{item.ItemID},
	}
	return plan, snapshot, graph
}

func assertChangePlanIssue(t *testing.T, result changeplanmodel.BusinessChangePlanValidation, code string) {
	t.Helper()
	for _, issue := range result.Issues {
		if issue.Code == code {
			return
		}
	}
	t.Fatalf("missing issue %q in %#v", code, result.Issues)
}

func assertValidatorIssue(t *testing.T, validator *businessChangePlanValidator, code string) {
	t.Helper()
	assertChangePlanIssue(t, validator.result, code)
}
