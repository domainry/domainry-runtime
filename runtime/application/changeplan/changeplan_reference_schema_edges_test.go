package changeplan

import (
	"fmt"
	"reflect"
	"testing"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type changePlanReferenceStringer string

func (value changePlanReferenceStringer) String() string { return fmt.Sprint(string(value)) }

func TestChangePlanRichReferenceSchemaCoversGenericResourceRelationships(t *testing.T) {
	condition := &definitionmodel.WorkflowConditionContract{Field: "status", Value: "approved", Expression: "$record.amount > 0", Conditions: []definitionmodel.WorkflowConditionContract{{Field: "owner_id", Value: "user-1"}}, Condition: &definitionmodel.WorkflowConditionContract{Expression: "$before.status != $record.status"}}
	snapshot := ReferenceSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "order", Name: "Order", Fields: []definitionmodel.FieldSchema{
				{Key: "status", Name: "Status", Validation: definitionmodel.FieldValidation{Options: []string{"draft", "approved"}}, Options: []any{"approved", "cancelled", ""}},
				{Key: "customer_id", Name: "Customer", Config: map[string]any{"object_key": "customer"}},
				{Key: "owner_id", Name: "Owner", Config: map[string]any{"target": "identity_user"}},
				{Key: "fallback_relation", Name: "Fallback", Validation: definitionmodel.FieldValidation{Target: "customer"}},
			}},
			{Key: "customer", Name: "Customer"},
		},
		Actions: []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order", Label: "Approve", RequiresPermission: "order.update"}},
		Workflows: []definitionmodel.WorkflowSchema{{
			Key: "order_approval", Name: "Order approval", RunAs: "manager",
			TriggerContract:   &definitionmodel.WorkflowTriggerContract{ObjectKeys: []string{"", "order"}, FieldKey: "status"},
			ConditionContract: condition,
			ActionContract:    &definitionmodel.WorkflowActionContract{ObjectKey: "order", ActionKey: "order.approve"},
			Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{
				{ID: "empty"},
				{ID: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: condition}},
				{ID: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.notify", ObjectKey: "order", Input: map[string]any{"owner": "$record.owner_id"}}}},
				{ID: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{ReminderActionKey: "order.remind", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{RoleKey: "manager", UserField: "owner_id"}, {RoleKey: "auditor", Field: "reviewer_id"}}}}},
				{ID: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "order.cc", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{RoleKey: "observer", Field: "owner_id"}}}}},
			}},
		}},
		AutomationRules: []automationmodel.AutomationRuleSchema{{
			Key: "order_after_update", Name: "Order update", ObjectKey: "order", Execution: automationmodel.AutomationExecutionPolicy{RunAs: "automation_operator"},
			Trigger:    automationmodel.AutomationTriggerSchema{ChangedFields: []string{"status"}, FromState: "draft", ToState: "approved"},
			Conditions: automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$record.status", Value: "approved"}, {Reference: "$record.amount", Value: 10}}, Groups: []automationmodel.AutomationConditionGroup{{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$before.owner_id", Value: "user"}}}}},
			Instructions: []automationmodel.AutomationInstructionSchema{
				{Type: "invoke_business_action", Config: map[string]any{"action_key": "order.approve", "input": "$record.status"}},
				{Type: "start_workflow", Config: map[string]any{"workflow_key": "order_approval"}},
				{Type: "connector_call", ConnectorKey: "erp", Operation: "send", Input: map[string]any{"id": "$record.customer_id"}},
				{Type: "enqueue_outbox", ConnectorKey: "erp", Operation: "enqueue"},
			},
		}},
		Reports:      []reportmodel.ReportSchema{{Key: "orders", Name: "Orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "missing", Alias: "missing"}}}, RequiredPermissions: []string{"order.read"}}},
		Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "erp", Name: "", Source: "builtin", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send", Name: "", CompensationOperation: "cancel"}, {Key: "cancel", Name: "Cancel"}}}}},
		Agents:       []agentsdk.AgentSchema{{Key: "sales_agent", Name: "Sales", Config: map[string]any{"report_keys": []any{"orders", "pipeline"}}}},
	}

	builder := NewReferenceGraphBuilder()
	AddSchemaReferences(builder, snapshot)
	AddActionReferences(builder, snapshot)
	AddWorkflowReferences(builder, snapshot)
	AddAutomationReferences(builder, snapshot)
	AddReportIntegrationReferences(builder, snapshot)
	AddPresentationReferences(builder, snapshot)
	graph := builder.Graph()
	if len(graph.Nodes) < 34 || len(graph.Edges) < 64 || graph.Hash == "" {
		t.Fatalf("rich graph too small: nodes=%d edges=%d hash=%q", len(graph.Nodes), len(graph.Edges), graph.Hash)
	}
}

func TestChangePlanReferenceHelpersCoverEmptyEdges(t *testing.T) {
	builder := NewReferenceGraphBuilder()
	addWorkflowTimerReferences(builder, "workflow", "", "wait", definitionmodel.WorkflowTimerNodeContract{}, "timer")
	addWorkflowTimerReferences(builder, "workflow", "order", "wait", definitionmodel.WorkflowTimerNodeContract{TimerKey: "timer", SourceField: "", BusinessCalendarKey: ""}, "timer")
	addStateValueMapReferences(builder, "action", "update", "order", map[string]any{" ": "ignored"}, "values")
	graph := builder.Graph()
	if graph.Hash == "" {
		t.Fatal("reference helper graph was not finalized")
	}
}

func TestChangePlanReferenceGraphTracksBusinessProfileReportJoinAndTimerFieldsExactly(t *testing.T) {
	snapshot := ReferenceSchema{
		ProfileBindings: []profilebindingmodel.Binding{{ObjectKey: "operator_profile", IdentityRelationField: "identity_user_id", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "operator", Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: "territory_id", FieldKey: "territory_id"}}}, SummaryFields: []string{"display_name"}}},
		Reports:         []reportmodel.ReportSchema{{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "account", Alias: "accounts", LeftAlias: "orders", LeftField: "account_id", RightField: "id"}}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "status"}}}}}},
		Workflows:       []definitionmodel.WorkflowSchema{{Key: "order.follow_up", TriggerContract: &definitionmodel.WorkflowTriggerContract{ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "wait", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{TimerKey: "order.follow_up.wait", SourceField: "due_at", BusinessCalendarKey: "default"}}}}}}},
	}
	builder := NewReferenceGraphBuilder()
	AddIdentityProfileReferences(builder, snapshot)
	AddReportIntegrationReferences(builder, snapshot)
	AddWorkflowReferences(builder, snapshot)
	graph := builder.Graph()
	want := map[string]bool{
		"report:orders->field:order.account_id:joins_on_field":                                           false,
		"report:orders->field:account.id:joins_on_field":                                                 false,
		"report:orders->field:order.status:groups_by_field":                                              false,
		"timer:order.follow_up.wait->field:order.due_at:reads_schedule_field":                            false,
		"identity_profile_binding:operator_profile->field:operator_profile.territory_id:publishes_claim": false,
	}
	for _, edge := range graph.Edges {
		key := edge.FromType + ":" + edge.FromKey + "->" + edge.ToType + ":" + edge.ToKey + ":" + edge.Kind
		if _, exists := want[key]; exists {
			want[key] = true
		}
		if edge.FromType == "report" && edge.FromKey == "orders" && edge.ToKey == "order.unused" {
			t.Fatalf("report graph invented an unused field edge: %#v", edge)
		}
	}
	for edge, found := range want {
		if !found {
			t.Errorf("missing reference edge %s", edge)
		}
	}
}

func TestChangePlanReferenceConversionAndExpressionEdges(t *testing.T) {
	if got := businessFieldOptionValues(definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Options: []string{"a", "a", ""}}, Options: []string{"b"}}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("options = %+v", got)
	}
	if referenceRelationTarget(definitionmodel.FieldSchema{Config: map[string]any{"object_key": " customer "}}) != "customer" || referenceRelationTarget(definitionmodel.FieldSchema{}) != "" {
		t.Fatal("relation target mismatch")
	}
	if referenceConfigString(nil, "missing") != "" || referenceValueOrDefault(" value ", "fallback") != "value" || referenceValueOrDefault("", "fallback") != "fallback" {
		t.Fatal("reference string normalization mismatch")
	}
	if got := referenceStringList([]any{"a", 2}); !reflect.DeepEqual(got, []string{"a", "2"}) || len(referenceStringList(3)) != 0 {
		t.Fatalf("string list = %+v", got)
	}
	if got := referenceMap(nil); len(got) != 0 || referenceMap(map[string]any{"a": 1})["a"] != 1 {
		t.Fatalf("map = %+v", got)
	}
	if got := referenceMapSlice([]any{map[string]any{"a": 1}, "ignored", map[string]any{}}); len(got) != 1 {
		t.Fatalf("map slice = %+v", got)
	}
	if got := referenceMapSlice([]map[string]any{{"a": 1}}); len(got) != 1 || referenceMapSlice("bad") != nil {
		t.Fatalf("typed map slice = %+v", got)
	}
	if stringIndex(12) != "12" {
		t.Fatal("stringIndex mismatch")
	}

	builder := NewReferenceGraphBuilder()
	addExpressionFieldReferences(builder, "action", "a", "order", nil, "nil")
	addExpressionFieldReferences(builder, "action", "a", "", "$record.status", "empty")
	addExpressionFieldReferences(builder, "action", "a", "order", []any{"$record.status", map[string]any{"x": "$before.owner_id"}, 12, "$candidate.customer_id"}, "input")
	addStateValueMapReferences(builder, "action", "a", "order", map[string]any{"status": "approved", "dynamic": "$record.status", "empty": "", "number": 1}, "data")
	addConnectorOperationEdge(builder, "action", "a", map[string]any{}, "empty")
	addConnectorOperationEdge(builder, "action", "a", map[string]any{"connector_key": "erp", "operation": "send"}, "call")
	if graph := builder.Graph(); len(graph.Edges) != 9 {
		t.Fatalf("expression graph edges=%d: %+v", len(graph.Edges), graph.Edges)
	}
}

func TestChangePlanReferenceEmptyAndFallbackBranches(t *testing.T) {
	builder := NewReferenceGraphBuilder()
	addExpressionFieldReferences(builder, "action", "a", "order", changePlanReferenceStringer("$record.status"), "stringer")
	addExpressionFieldReferences(builder, "action", "a", "order", 12, "number")
	AddSchemaReferences(builder, ReferenceSchema{Objects: []definitionmodel.ObjectSchema{{Key: "plain", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}})
	AddActionReferences(builder, ReferenceSchema{Actions: []definitionmodel.ActionSchema{{Key: "empty-target", ObjectKey: "order"}}})

	AddAutomationReferences(builder, ReferenceSchema{AutomationRules: []automationmodel.AutomationRuleSchema{
		{Key: "empty", ObjectKey: "order", Execution: automationmodel.AutomationExecutionPolicy{RunAs: ""}},
		{Key: "initiator", ObjectKey: "order", Execution: automationmodel.AutomationExecutionPolicy{RunAs: "initiator"}, Trigger: automationmodel.AutomationTriggerSchema{ChangedFields: []string{"a", "b"}}, Conditions: automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "plain", Value: "x"}, {Reference: "$record.status", Value: 1}, {Reference: "$record.owner", Value: " "}}}},
	}})

	conditionWithoutObject := &definitionmodel.WorkflowConditionContract{Field: "", Value: 1}
	AddWorkflowReferences(builder, ReferenceSchema{Workflows: []definitionmodel.WorkflowSchema{
		{Key: "empty", RunAs: "", Graph: nil},
		{Key: "initiator", RunAs: "initiator", TriggerContract: &definitionmodel.WorkflowTriggerContract{ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
		{Key: "field-without-object", TriggerContract: &definitionmodel.WorkflowTriggerContract{FieldKey: "status"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
		{Key: "action-fallback", TriggerContract: nil, ActionContract: &definitionmodel.WorkflowActionContract{ObjectKey: "order"}, ConditionContract: conditionWithoutObject, Graph: &definitionmodel.WorkflowGraphSchema{}},
	}})
	addWorkflowTriggerReferences(builder, definitionmodel.WorkflowSchema{Key: "no-trigger"})
	addWorkflowConditionReferences(builder, "workflow", "", &definitionmodel.WorkflowConditionContract{Field: "status", Value: "approved"}, "condition")
	addWorkflowConditionReferences(builder, "workflow", "order", &definitionmodel.WorkflowConditionContract{Field: "", Value: "approved"}, "condition")
	addWorkflowConditionReferences(builder, "workflow", "order", &definitionmodel.WorkflowConditionContract{Field: "status", Value: 1}, "condition")
	addWorkflowConditionReferences(builder, "workflow", "order", &definitionmodel.WorkflowConditionContract{Field: "status", Value: " "}, "condition")
	addWorkflowResolverReferences(builder, "workflow", "", definitionmodel.WorkflowAssigneeResolver{Field: "owner"}, "resolver")
	addWorkflowResolverReferences(builder, "workflow", "order", definitionmodel.WorkflowAssigneeResolver{}, "resolver")

	addConnectorOperationEdge(builder, "action", "empty-connector", map[string]any{"connector_key": "", "operation": "send"}, "call")
	addConnectorOperationEdge(builder, "action", "nil-connector", map[string]any{"connector_key": nil, "operation": "send"}, "call")
	addConnectorOperationEdge(builder, "action", "empty-operation", map[string]any{"connector_key": "erp", "operation": ""}, "call")
	addConnectorOperationEdge(builder, "action", "nil-operation", map[string]any{"connector_key": "erp", "operation": "<nil>"}, "call")
	if graph := builder.Graph(); graph.Hash == "" {
		t.Fatal("fallback graph hash is empty")
	}
}
