package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestWorkflowTriggerAndApprovalReferenceEdges(t *testing.T) {
	graph := func(node definitionmodel.WorkflowGraphNode) *definitionmodel.WorkflowGraphSchema {
		return &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{node}}
	}
	invalidCondition := &definitionmodel.WorkflowConditionContract{Type: "field_equals"}
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "known", Fields: []definitionmodel.FieldSchema{{Key: "field"}}}},
		Actions: []definitionmodel.ActionSchema{{Key: "known-action"}},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "empty-trigger", TriggerContract: &definitionmodel.WorkflowTriggerContract{}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "field", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "field_changed", ObjectKey: "known", FieldKey: "missing"}, ConditionContract: invalidCondition, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "after", Trigger: map[string]any{"type": "after_update"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "lifecycle", Trigger: map[string]any{"type": "lifecycle_update"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "missing-object", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual", ObjectKey: "missing"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "due", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{DueSeconds: -1}}})},
			{Key: "escalation", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{EscalationSeconds: -1}}})},
			{Key: "action-object", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: graph(definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "known-action", ObjectKey: "missing"}}})},
		},
	}, nil)
	state.validateWorkflows()
	if len(state.errs) == 0 {
		t.Fatal("invalid workflow edges produced no diagnostics")
	}
	if got := manifestWorkflowApprovalNodeContract(definitionmodel.WorkflowGraphNode{}); got.Mode != "" {
		t.Fatalf("nil node contract=%+v", got)
	}
	if got := manifestWorkflowApprovalNodeContract(definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{}}); got.Mode != "" {
		t.Fatalf("nil approval contract=%+v", got)
	}
}

func TestManifestWorkflowActionPermissionAndResolverEdges(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "known", Fields: []definitionmodel.FieldSchema{{Key: "field"}, {Key: "disabled", DisabledAt: "now"}}},
			{Key: "second", Fields: []definitionmodel.FieldSchema{{Key: "field"}}},
		},
		Actions: []definitionmodel.ActionSchema{
			{Key: "blank-permission", ObjectKey: "known"},
			{Key: "allowed-by-action", ObjectKey: "known", Label: "approve"},
		},
	}, nil)
	workflow := definitionmodel.WorkflowSchema{RunAs: "role", TriggerContract: &definitionmodel.WorkflowTriggerContract{ObjectKey: "known", ObjectKeys: []string{"missing", "second"}}}
	state.validateWorkflowActionReference("missing", workflow, "missing", nil)
	state.validateWorkflowActionReference("blank", workflow, "blank-permission", nil)
	state.validateWorkflowActionReference("allowed", workflow, "allowed-by-action", nil)

	state.actions["registered-empty"] = definitionmodel.ActionSchema{ObjectKey: "known"}
	state.validateWorkflowActionReference("empty-name", workflow, "registered-empty", nil)
	state.validateWorkflowResolvers("resolver", workflow, []definitionmodel.WorkflowAssigneeResolver{
		{Type: "role", RoleKey: "missing"},
		{Type: "record_field", Field: "missing"},
		{Type: "manager", UserField: "missing"},
		{Type: "manager_of", UserField: "disabled"},
	})
	if len(state.errs) < 4 {
		t.Fatalf("workflow reference diagnostics=%+v", state.errs)
	}

	if state.workflowTriggerObjectsHaveField(definitionmodel.WorkflowSchema{}, "") {
		t.Fatal("blank field accepted without trigger objects")
	}
	if !state.workflowTriggerObjectsHaveField(definitionmodel.WorkflowSchema{}, "field") {
		t.Fatal("nonblank field rejected without trigger objects")
	}
	if !state.workflowTriggerObjectsHaveField(workflow, "field") {
		t.Fatal("field on later trigger object not found")
	}
}

func TestManifestWorkflowObjectKeyAndSystemFieldEdges(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "known", Fields: []definitionmodel.FieldSchema{{Key: "custom"}}}},
	}, nil)
	if keys := manifestWorkflowTriggerObjectKeys(definitionmodel.WorkflowSchema{}); len(keys) != 0 {
		t.Fatalf("nil trigger keys=%v", keys)
	}
	keys := manifestWorkflowTriggerObjectKeys(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{
		ObjectKey: " ", ObjectKeys: []string{"known", "known", ""},
	}})
	if len(keys) != 1 || keys[0] != "known" {
		t.Fatalf("normalized trigger keys=%v", keys)
	}
	for _, field := range []string{"id", "created_at", "updated_at", "custom"} {
		if !state.workflowObjectHasField("known", field) {
			t.Fatalf("known field %q not found", field)
		}
	}
	if state.workflowObjectHasField("missing", "id") || state.workflowObjectHasField("known", "missing") {
		t.Fatal("unknown workflow field accepted")
	}
}
