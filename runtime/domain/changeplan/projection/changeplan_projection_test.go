package projection

import (
	"encoding/json"
	"reflect"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestChangePlanDiffProjection(t *testing.T) {
	graphBuilder := NewChangePlanReferenceGraphBuilder()
	graphBuilder.Node(" field ", " order.total ", "order", "Old", "owner")
	graphBuilder.Node("field", "order.total", "", "New", "")
	graphBuilder.Node("", "missing", "", "", "")
	graphBuilder.Node("field", "", "", "", "")
	graphBuilder.Edge("scheduler_job", "job", "field", "order.total", "reads", "spec.field")
	graphBuilder.Edge("outbox_message", "outbox", "scheduler_job", "job", "runs", "")
	graphBuilder.Edge("frontend_surface", "screen", "field", "order.total", "renders", "")
	graphBuilder.Edge("seed_record", "seed", "field", "order.total", "seeds", "")
	graphBuilder.Edge("view", "view", "field", "order.total", "renders", "")
	graphBuilder.Edge("", "", "field", "order.total", "bad", "")
	graphBuilder.Edge("field", "order.total", "object", "", "bad", "")
	graph := graphBuilder.Graph()
	if graph.Version != ChangePlanReferenceGraphVersion || graph.Hash == "" || len(graph.Nodes) != 6 || len(graph.Edges) != 5 {
		t.Fatalf("unexpected graph: %#v", graph)
	}
	if graph.Nodes[0].ResourceKey > graph.Nodes[len(graph.Nodes)-1].ResourceKey && graph.Nodes[0].ResourceType == graph.Nodes[len(graph.Nodes)-1].ResourceType {
		t.Fatal("nodes not sorted")
	}
	impact := ChangePlanReferenceImpact(graph, "field", "order.total")
	if !impact.DeletionBlocked || len(impact.DirectConsumers) != 4 || len(impact.IndirectConsumers) != 1 {
		t.Fatalf("unexpected impact: %#v", impact)
	}

	item := changeplanmodel.BusinessSystemChangeItem{
		ItemID: "item", Operation: "update", ChangeKind: "breaking", ResourceType: "field", ResourceKey: "order.total",
		Before: json.RawMessage(`{"name":"Old total","type":"string","required":false,"removed":"x"}`),
		After:  json.RawMessage(`{"label":"Total","type":"number","required":true,"added":"y"}`), FrontendSupportKey: "screen",
	}
	diff := BuildChangePlanBusinessDiff(item, changeplanmodel.Snapshot{ObjectRecordCounts: map[string]int{"order": 2}}, graph)
	if diff.AffectedRecords != 2 || len(diff.Changes) != 6 || !reflect.DeepEqual(diff.RiskSignals, []string{"business_interruption", "data_backfill_required", "existing_records_impact", "frontend_compatibility", "outbox_delivery_impact", "scheduler_execution_impact", "seed_data_impact"}) {
		t.Fatalf("unexpected diff: %#v", diff)
	}

	cases := []struct {
		item changeplanmodel.BusinessSystemChangeItem
		want string
	}{
		{changeplanmodel.BusinessSystemChangeItem{Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"title":"Order"}`)}, "Create domain object Order (order)"},
		{changeplanmodel.BusinessSystemChangeItem{Operation: "noop", ResourceType: "strange_kind", ResourceKey: "same", After: json.RawMessage(`{"name":"same"}`)}, "Keep strange kind same"},
		{changeplanmodel.BusinessSystemChangeItem{Operation: "other", ResourceType: "menu", ResourceKey: "menu"}, "Change menu menu"},
	}
	for _, tc := range cases {
		if got := ChangePlanBusinessSummary(tc.item); got != tc.want {
			t.Errorf("summary %q, want %q", got, tc.want)
		}
	}
	if got := normalizeBusinessChangeJSON(json.RawMessage(` {"b":2,"a":1} `)); string(got) != `{"a":1,"b":2}` {
		t.Fatalf("normalized = %s", got)
	}
	invalid := json.RawMessage(`{`)
	if got := normalizeBusinessChangeJSON(invalid); string(got) != "{" || businessChangeJSONValue(got) != nil {
		t.Fatalf("invalid JSON handling: %q", got)
	}
	for _, empty := range []json.RawMessage{nil, json.RawMessage(" "), json.RawMessage("null")} {
		if normalizeBusinessChangeJSON(empty) != nil || businessChangeJSONValue(empty) != nil || rawJSONPresent(empty) {
			t.Fatalf("expected absent JSON: %q", empty)
		}
	}
	changes := collectBusinessPropertyChanges("", map[string]any{"b": 1.0, "a": 1.0}, map[string]any{"b": 1.0, "a": 2.0})
	if len(changes) != 1 || changes[0].Path != "a" || changes[0].Kind != "replace" || len(collectBusinessPropertyChanges("", 1.0, 1.0)) != 0 {
		t.Fatalf("unexpected property changes: %#v", changes)
	}
	root := collectBusinessPropertyChanges("", nil, "x")
	removed := collectBusinessPropertyChanges("nested", "x", nil)
	if root[0].Path != "$" || root[0].Kind != "add" || removed[0].Kind != "remove" {
		t.Fatalf("unexpected root/remove changes: %#v %#v", root, removed)
	}
	nested := collectBusinessPropertyChanges("parent", map[string]any{"child": 1.0}, map[string]any{"child": 2.0})
	if nested[0].Path != "parent.child" {
		t.Fatalf("nested path = %#v", nested)
	}
	if got := collectBusinessPropertyChanges("mixed", map[string]any{"child": 1.0}, "scalar"); len(got) != 1 || got[0].Kind != "replace" {
		t.Fatalf("mixed change = %#v", got)
	}

	permission := changeplanmodel.BusinessSystemChangeItem{ResourceType: "role", Before: json.RawMessage(`{"permissions":["read","remove"]}`), After: json.RawMessage(`{"permissions":["read","add"]}`)}
	if !businessChangePermissionExpanded(permission) || !businessChangePermissionReduced(permission) {
		t.Fatal("expected permission expansion and reduction")
	}
	if businessChangePermissionExpanded(changeplanmodel.BusinessSystemChangeItem{ResourceType: "object"}) {
		t.Fatal("object must not be treated as permission")
	}
	permissionSignals := businessChangeRiskSignals(permission, changeplanmodel.Snapshot{}, changeplanmodel.ReferenceImpact{})
	if !reflect.DeepEqual(permissionSignals, []string{"permission_expansion", "permission_reduction"}) {
		t.Fatalf("permission signals = %#v", permissionSignals)
	}
	if businessChangePermissionExpanded(changeplanmodel.BusinessSystemChangeItem{ResourceType: "role", Before: json.RawMessage(`{"p":"read"}`), After: json.RawMessage(`{"p":"read"}`)}) {
		t.Fatal("equivalent permission set reported expansion")
	}
	workflowSignals := businessChangeRiskSignals(changeplanmodel.BusinessSystemChangeItem{ResourceType: "workflow", ResourceKey: "wf"}, changeplanmodel.Snapshot{RuntimeState: changeplanmodel.RuntimeState{RunningWorkflowProcesses: []changeplanmodel.WorkflowProcess{{WorkflowKey: "other"}, {WorkflowKey: "wf"}}}}, changeplanmodel.ReferenceImpact{})
	if !reflect.DeepEqual(workflowSignals, []string{"workflow_running_instances"}) {
		t.Fatalf("workflow signals = %#v", workflowSignals)
	}
	noBackfill := businessChangeRiskSignals(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "order.total", Operation: "create"}, changeplanmodel.Snapshot{}, changeplanmodel.ReferenceImpact{})
	requiredBackfill := businessChangeRiskSignals(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "order.total", Operation: "update", Before: json.RawMessage(`{"type":"string","required":false}`), After: json.RawMessage(`{"type":"string","required":true}`)}, changeplanmodel.Snapshot{ObjectRecordCounts: map[string]int{}}, changeplanmodel.ReferenceImpact{})
	unchangedField := businessChangeRiskSignals(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "order.total", Operation: "update", Before: json.RawMessage(`{"type":"string","required":false}`), After: json.RawMessage(`{"type":"string","required":false}`)}, changeplanmodel.Snapshot{}, changeplanmodel.ReferenceImpact{})
	if len(noBackfill) != 0 || len(unchangedField) != 0 || !reflect.DeepEqual(requiredBackfill, []string{"data_backfill_required"}) {
		t.Fatalf("backfill signals = %#v / %#v", noBackfill, requiredBackfill)
	}
	for _, interrupting := range []changeplanmodel.BusinessSystemChangeItem{{Operation: "archive"}, {Operation: "delete"}, {ChangeKind: "destructive"}} {
		if got := businessChangeRiskSignals(interrupting, changeplanmodel.Snapshot{}, changeplanmodel.ReferenceImpact{}); !reflect.DeepEqual(got, []string{"business_interruption"}) {
			t.Fatalf("interruption signals = %#v", got)
		}
	}
	_ = businessChangeRiskSignals(changeplanmodel.BusinessSystemChangeItem{ResourceType: "object"}, changeplanmodel.Snapshot{RuntimeState: changeplanmodel.RuntimeState{RunningWorkflowProcesses: []changeplanmodel.WorkflowProcess{{WorkflowKey: "wf"}}}}, changeplanmodel.ReferenceImpact{})
	if businessChangePermissionExpanded(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field_permission", Before: json.RawMessage(`[]`), After: json.RawMessage(`["read"]`)}) != true {
		t.Fatal("field permission expansion missing")
	}
	if businessChangePermissionExpanded(changeplanmodel.BusinessSystemChangeItem{ResourceType: "data_scope", Before: json.RawMessage(`[]`), After: json.RawMessage(`["own"]`)}) != true {
		t.Fatal("data scope expansion missing")
	}
	if got := flattenBusinessChangeStrings(map[string]any{"a": []any{"x", 1.0}, "b": map[string]any{"c": "y"}}); !got["x"] || !got["y"] || len(got) != 2 {
		t.Fatalf("flattened = %#v", got)
	}
	if businessChangeObjectKey(changeplanmodel.BusinessSystemChangeItem{ResourceType: "object", ResourceKey: "order"}) != "order" || businessChangeObjectKey(changeplanmodel.BusinessSystemChangeItem{ResourceType: "role"}) != "" {
		t.Fatal("object key projection mismatch")
	}
	if businessChangeObjectKey(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "total", After: json.RawMessage(`{"object_key":" order "}`)}) != "order" {
		t.Fatal("field object key fallback mismatch")
	}
	if businessChangeObjectKey(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "total", Before: json.RawMessage(`{"_definition_object_key":"legacy"}`)}) != "legacy" {
		t.Fatal("legacy field object key fallback mismatch")
	}
	if businessChangeObjectKey(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", ResourceKey: "total"}) != "" {
		t.Fatal("missing field object key must be empty")
	}
	if !businessFieldBecameRequired(item) || businessFieldBecameRequired(changeplanmodel.BusinessSystemChangeItem{ResourceType: "object"}) || businessFieldBecameRequired(changeplanmodel.BusinessSystemChangeItem{ResourceType: "field", Before: json.RawMessage(`{"required":"no"}`), After: json.RawMessage(`{"required":true}`)}) {
		t.Fatal("required transition mismatch")
	}
	if stringValue("x") != "x" || stringValue(1) != "" || businessChangeJSONMap(json.RawMessage(`{`)) == nil {
		t.Fatal("JSON helpers mismatch")
	}
}

func TestRuntimeStateProjectionAndNormalization(t *testing.T) {
	processes := ProjectWorkflowProcesses([]workflowmodel.WorkflowProcessInstance{
		{ID: "3", Status: "complete"}, {ID: "2", WorkflowKey: "wf", Status: "waiting", CurrentNodeIDs: []string{"n"}}, {ID: "1", Status: "running"}, {ID: "4", Status: "configuration_error"},
	})
	if len(processes) != 3 || processes[0].ID != "2" {
		t.Fatalf("processes = %#v", processes)
	}
	connections := []integrationmodel.IntegrationConnection{{Key: "b"}, {Key: "a"}}
	withoutReady := ProjectIntegrationConnections(connections, nil)
	withReady := ProjectIntegrationConnections(connections, func(connection integrationmodel.IntegrationConnection) bool { return connection.Key == "b" })
	if withoutReady[0].Ready || withReady[0].Ready || !withReady[1].Ready || withReady[0].Key != "a" {
		t.Fatalf("connections = %#v / %#v", withoutReady, withReady)
	}
	outbox := ProjectIntegrationOutbox([]integrationmodel.IntegrationOutboxMessage{{ID: "old", UpdatedAt: "1"}, {ID: "new", UpdatedAt: "2"}})
	if outbox[0].ID != "new" {
		t.Fatalf("outbox = %#v", outbox)
	}

	empty := BusinessRuntimeStateSnapshot{}
	empty.Normalize()
	if empty.Idempotency.Backlog == nil || empty.RunningWorkflowProcesses == nil || empty.AutomationRules == nil || empty.RecentAutomationRuns == nil || empty.Scheduler.Definitions == nil || empty.Scheduler.RecentRuns == nil || empty.Scheduler.DeadLetters == nil || empty.Reports == nil || empty.Connectors == nil || empty.Connections == nil || empty.RecentOutboxMessages == nil {
		t.Fatalf("nil collection survived normalization: %#v", empty)
	}
	full := BusinessRuntimeStateSnapshot{
		RunningWorkflowProcesses: []WorkflowProcessSummary{{ID: "b"}, {ID: "a"}},
		AutomationRules:          []automationmodel.AutomationRuleSchema{{Key: "b"}, {Key: "a"}},
		RecentAutomationRuns:     []automationmodel.AutomationRuleExecution{{ID: "b"}, {ID: "a"}},
		Scheduler:                SchedulerStateSnapshot{Definitions: []recordmodel.Record{{ID: "b"}, {ID: "a"}}, RecentRuns: []recordmodel.Record{{ID: "b"}, {ID: "a"}}, DeadLetters: []recordmodel.Record{{ID: "b"}, {ID: "a"}}},
		Reports:                  []reportmodel.ReportSchema{{Key: "b"}, {Key: "a"}}, Connectors: []integrationmodel.ConnectorSchema{{Key: "b"}, {Key: "a"}},
		Connections: []IntegrationConnectionSummary{}, RecentOutboxMessages: []IntegrationOutboxSummary{},
	}
	full.Normalize()
	if full.RunningWorkflowProcesses[0].ID != "a" || full.AutomationRules[0].Key != "a" || full.RecentAutomationRuns[0].ID != "a" || full.Scheduler.Definitions[0].ID != "a" || full.Scheduler.RecentRuns[0].ID != "a" || full.Scheduler.DeadLetters[0].ID != "a" || full.Reports[0].Key != "a" || full.Connectors[0].Key != "a" {
		t.Fatalf("collections not sorted: %#v", full)
	}
}

func TestBusinessSystemSnapshotProjection(t *testing.T) {
	snapshot := BusinessSystemSnapshot{
		RuntimeVersion: "1", CapabilityKeys: []string{"cap"}, HiddenResourceCategories: []string{"z", "a"},
		ResourceSources: []SystemResourceSource{{ResourceType: "z", ResourceKey: "b"}, {ResourceType: "a", ResourceKey: "b"}, {ResourceType: "a", ResourceKey: "a"}},
		SeedRecords:     []businessseedmodel.BusinessSeedProvenance{{ObjectKey: "z", SeedKey: "b"}, {ObjectKey: "a", SeedKey: "a"}},
		RuntimeState:    BusinessRuntimeStateSnapshot{RunningWorkflowProcesses: []WorkflowProcessSummary{{WorkflowKey: "wf", Status: "running"}}, Connectors: []integrationmodel.ConnectorSchema{{Key: "c"}}, Connections: []IntegrationConnectionSummary{{Key: "conn", Ready: true}}},
	}
	snapshot.Finalize()
	if snapshot.SnapshotHash == "" || snapshot.ObjectRecordCounts == nil {
		t.Fatalf("snapshot not finalized: %#v", snapshot)
	}
	duplicateLen := len(snapshot.ResourceSources)
	snapshot.ResourceSources = AddSystemResourceSource(snapshot.ResourceSources, "a", "a", "changed", "manual")
	if len(snapshot.ResourceSources) != duplicateLen {
		t.Fatal("duplicate resource source appended")
	}
	projected := snapshot.ChangePlanSnapshot()
	if len(projected.ResourceSources) != duplicateLen || len(projected.RuntimeState.RunningWorkflowProcesses) != 1 || len(projected.RuntimeState.Connections) != 1 || len(projected.CapabilityKeys) != 1 {
		t.Fatalf("projected = %#v", projected)
	}

	metadata := RuntimeNativeMetadataModel{TemplateID: " tpl ", TemplateVersion: " v1 "}
	snapshot = snapshot.WithRuntimeMetadata(metadata)
	if SystemSnapshotHash(snapshot) == "" {
		t.Fatal("empty hash")
	}
}

func TestBusinessSystemSnapshotFinalizeNormalizesPublishedCollections(t *testing.T) {
	snapshot := BusinessSystemSnapshot{}
	snapshot.Finalize()
	if snapshot.ResourceSources == nil || snapshot.SeedRecords == nil || snapshot.HiddenResourceCategories == nil {
		t.Fatalf("snapshot contains nil published collections: %#v", snapshot)
	}
}

func TestSystemSnapshotHashExcludesPrincipalSpecificPermissions(t *testing.T) {
	maker := BusinessSystemSnapshot{SnapshotVersion: BusinessSystemSnapshotVersion, EffectivePermissions: recordcontract.RecordFeaturePermissionSnapshot{UserID: "maker"}}
	approver := maker
	approver.EffectivePermissions = recordcontract.RecordFeaturePermissionSnapshot{UserID: "approver"}
	if SystemSnapshotHash(maker) != SystemSnapshotHash(approver) {
		t.Fatal("maker and approver received different system concurrency hashes from principal-specific permissions")
	}
	approver.ResourceSources = []SystemResourceSource{{ResourceType: "field", ResourceKey: "customer.name"}}
	if SystemSnapshotHash(maker) == SystemSnapshotHash(approver) {
		t.Fatal("actor-independent business resource change was omitted from the system hash")
	}
}

func TestSystemSnapshotHashExcludesIdempotencyOperationalChurn(t *testing.T) {
	base := BusinessSystemSnapshot{
		SnapshotVersion: BusinessSystemSnapshotVersion,
		RuntimeState: BusinessRuntimeStateSnapshot{
			Idempotency: deploymentmodel.IdempotencyOperationalStatus{
				Backlog: map[string]int{"in_progress": 1},
			},
		},
	}
	changed := base
	changed.RuntimeState.Idempotency = deploymentmodel.IdempotencyOperationalStatus{
		Backlog: map[string]int{"succeeded": 99},
	}
	if SystemSnapshotHash(base) != SystemSnapshotHash(changed) {
		t.Fatal("idempotency operational churn changed the governed system snapshot hash")
	}
}
