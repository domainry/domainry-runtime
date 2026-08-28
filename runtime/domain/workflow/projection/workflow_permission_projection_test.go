package projection

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowPermissionProjectionPreservesAdvancedViews(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{DefinitionVersionID: "v1", DefinitionHash: "hash", ErrorCode: "internal"}
	if got := WorkflowProcessForPrincipal(process, true, nil); !reflect.DeepEqual(got, process) {
		t.Fatalf("advanced process changed: %#v", got)
	}
	task := workflowmodel.WorkflowTask{ResolverSnapshot: []definitionmodel.WorkflowAssigneeResolver{{Type: "role"}}, CandidateSource: "policy", NodeDefinitionVersion: 2}
	if got := WorkflowTaskForPrincipal(task, true); !reflect.DeepEqual(got, task) {
		t.Fatalf("advanced task changed: %#v", got)
	}
	node := workflowmodel.WorkflowNodeInstance{Input: map[string]any{"secret": true}, Output: map[string]any{"secret": true}, ErrorCode: "internal"}
	if got := WorkflowNodeForPrincipal(node, true); !reflect.DeepEqual(got, node) {
		t.Fatalf("advanced node changed: %#v", got)
	}
	event := workflowmodel.WorkflowProcessEvent{Metadata: map[string]any{"internal": true}}
	if got := WorkflowEventForPrincipal(event, true); !reflect.DeepEqual(got, event) {
		t.Fatalf("advanced event changed: %#v", got)
	}
}

func TestWorkflowPermissionProjectionRedactsInternalFields(t *testing.T) {
	workflow := permissionProjectionWorkflow()
	process := WorkflowProcessForPrincipal(workflowmodel.WorkflowProcessInstance{DefinitionVersionID: "v1", DefinitionHash: "hash", ErrorCode: "internal", DefinitionSnapshot: workflow}, false, permissionProjectionActions())
	if process.DefinitionVersionID != "" || process.DefinitionHash != "" || process.ErrorCode != "" {
		t.Fatalf("process internals remain: %#v", process)
	}
	assertBusinessWorkflow(t, process.DefinitionSnapshot)

	task := WorkflowTaskForPrincipal(workflowmodel.WorkflowTask{ResolverSnapshot: []definitionmodel.WorkflowAssigneeResolver{{Type: "role"}}, CandidateSource: "policy", NodeDefinitionVersion: 2, Title: "visible"}, false)
	if task.ResolverSnapshot != nil || task.CandidateSource != "" || task.NodeDefinitionVersion != 0 || task.Title != "visible" {
		t.Fatalf("task projection = %#v", task)
	}
	node := WorkflowNodeForPrincipal(workflowmodel.WorkflowNodeInstance{Input: map[string]any{"secret": true}, Output: map[string]any{"secret": true}, ErrorCode: "internal", Status: "failed"}, false)
	if node.Input != nil || node.Output != nil || node.ErrorCode != "" || node.Status != "failed" {
		t.Fatalf("node projection = %#v", node)
	}
	event := WorkflowEventForPrincipal(workflowmodel.WorkflowProcessEvent{Metadata: map[string]any{"secret": true}, Summary: "visible"}, false)
	if event.Metadata != nil || event.Summary != "visible" {
		t.Fatalf("event projection = %#v", event)
	}
}

func TestWorkflowNodeBusinessSummariesCoverNodeAndResolverKinds(t *testing.T) {
	actions := permissionProjectionActions()
	tests := []struct {
		node definitionmodel.WorkflowGraphNode
		key  string
		want any
	}{
		{node: definitionmodel.WorkflowGraphNode{Type: "trigger", Name: "Start"}, key: "business_summary_key", want: "workflow.node.trigger.summary"},
		{node: definitionmodel.WorkflowGraphNode{Type: "condition", Name: "Check"}, key: "business_summary_key", want: "workflow.node.condition.summary"},
		{node: definitionmodel.WorkflowGraphNode{Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "all", Resolvers: permissionProjectionResolvers()}}}, key: "approval_mode", want: "all"},
		{node: definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.update"}}}, key: "action_name", want: "Update order"},
		{node: definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.update"}}}, key: "action_key", want: "order.update"},
		{node: definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "missing"}}}, key: "action_name_key", want: "workflow.action.defaultName"},
		{node: definitionmodel.WorkflowGraphNode{Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "order.notify", Resolvers: permissionProjectionResolvers()}}}, key: "impact_summary_key", want: "workflow.action.recordUpdateImpact"},
		{node: definitionmodel.WorkflowGraphNode{Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{TimerKey: "wait", Purpose: "follow-up"}}}, key: "timer_key", want: "wait"},
		{node: definitionmodel.WorkflowGraphNode{Type: "unknown", Name: "Unknown"}, key: "business_name", want: "Unknown"},
	}
	for _, test := range tests {
		if got := workflowNodeBusinessSummary(test.node, actions)[test.key]; !reflect.DeepEqual(got, test.want) {
			t.Fatalf("summary for %s[%s] = %#v, want %#v", test.node.Type, test.key, got, test.want)
		}
	}
	if got, want := workflowAssigneeBusinessSummaryKeys(permissionProjectionResolvers()), []string{"workflow.assignee.configured", "workflow.assignee.manager", "workflow.assignee.manager", "workflow.assignee.manager", "workflow.assignee.recordOwner", "workflow.assignee.role", "workflow.assignee.users"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolver summaries = %#v, want %#v", got, want)
	}
}

func TestWorkflowBusinessDefinitionNilGraphAndExplicitEmptyImpact(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Name: "No graph"}
	if got := WorkflowBusinessDefinition(workflow, nil); got.Graph != nil || got.Name != "No graph" {
		t.Fatalf("nil graph business workflow = %#v", got)
	}
	name, nameKey, impact, impactKey := workflowActionBusinessDescription("empty", []definitionmodel.ActionSchema{{Key: "other"}, {Key: "empty", Label: "Empty"}})
	if name != "Empty" || nameKey != "" || impact != "" || impactKey != "workflow.action.recordUpdateImpact" {
		t.Fatalf("empty impact description = %q/%q/%q/%q", name, nameKey, impact, impactKey)
	}
	if got := workflowValueOrDefault(" ", "fallback"); got != "fallback" {
		t.Fatalf("fallback=%q", got)
	}
}

func permissionProjectionWorkflow() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{
		DefinitionVersionID: "v1", PublishedVersion: 2, Key: "order.approval", Name: "Order approval",
		Trigger: map[string]any{"type": "manual"}, Condition: map[string]any{"type": "always"}, Action: map[string]any{"type": "graph"},
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"}, ActionContract: &definitionmodel.WorkflowActionContract{Type: "graph"},
		RunAs: "system", IdempotencyKeys: []string{"record_id"}, Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3}, DeadLetterPolicy: map[string]any{"enabled": true}, TimeoutSeconds: 30, AuditEvent: "executed",
		Graph: &definitionmodel.WorkflowGraphSchema{Viewport: map[string]float64{"x": 1}, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "approval", Type: "approval", Name: "Review", Config: map[string]any{"internal": true}, Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: permissionProjectionResolvers()}}},
			{ID: "action", Type: "action", Name: "Execute", Config: map[string]any{"internal": true}, Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.update"}}},
		}},
	}
}

func permissionProjectionActions() []definitionmodel.ActionSchema {
	return []definitionmodel.ActionSchema{
		{Key: "order.update", Label: "Update order"},
		{Key: "order.notify", Label: "Notify"},
	}
}

func permissionProjectionResolvers() []definitionmodel.WorkflowAssigneeResolver {
	return []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}, {Type: "role"}, {Type: "manager"}, {Type: "manager_of"}, {Type: "initiator_manager"}, {Type: "record_field"}, {Type: "custom"}}
}

func assertBusinessWorkflow(t *testing.T, workflow definitionmodel.WorkflowSchema) {
	t.Helper()
	if workflow.DefinitionVersionID != "" || workflow.PublishedVersion != 0 || workflow.Trigger != nil || workflow.TriggerContract != nil || workflow.RunAs != "" || workflow.Retry != nil || workflow.TimeoutSeconds != 0 || workflow.AuditEvent != "" {
		t.Fatalf("workflow internals remain: %#v", workflow)
	}
	for _, node := range workflow.Graph.Nodes {
		if node.Contract != nil || node.Config["business_name"] == nil {
			t.Fatalf("node was not projected: %#v", node)
		}
	}
}
