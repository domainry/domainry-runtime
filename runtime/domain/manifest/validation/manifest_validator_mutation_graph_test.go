package validation

import (
	"reflect"
	"strings"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestValidateMutationGraphBuildsAllMutationEdgesAndRejectsCycle(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		AutomationRules: []automationmodel.AutomationRuleSchema{
			{
				Key:       "ignored",
				ObjectKey: "order",
				Trigger:   automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
			},
			{
				Key:       "after-order",
				ObjectKey: "order",
				Trigger:   automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"},
				Instructions: []automationmodel.AutomationInstructionSchema{
					{Type: "invoke_business_action", Config: map[string]any{"action_key": nil}},
					{Type: "start_workflow", Config: map[string]any{"workflow_key": " fulfillment "}},
					{Type: "ignored"},
				},
			},
		},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "nil-contract"},
			{Key: "empty-action", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "action_completed"}, Graph: nil},
			{
				Key:             "fulfillment",
				TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "action_executed", Event: " approve "},
				Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{
					{},
					{Contract: &definitionmodel.WorkflowNodeContract{
						Action:   &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: " approve "},
						Approval: &definitionmodel.WorkflowApprovalNodeContract{ReminderActionKey: " remind "},
						CC:       &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: " notify "},
					}},
				}},
			},
			{Key: "created", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_created", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
			{Key: "updated", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
			{Key: "deleted", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_deleted", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
			{Key: "restored", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_restored", ObjectKey: "order"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
			{Key: "event", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_event", Event: " archived ", ObjectKeys: []string{"order", "invoice"}}, Graph: &definitionmodel.WorkflowGraphSchema{}},
			{Key: "manual", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: &definitionmodel.WorkflowGraphSchema{}},
		},
	}, nil)

	state.validateMutationGraph()

	if len(state.errs) != 1 {
		t.Fatalf("mutation graph diagnostics=%+v", state.errs)
	}
	err := state.errs[0]
	if err.Path != "workflows[2].trigger_contract.event" {
		t.Fatalf("cycle path=%q", err.Path)
	}
	if !strings.Contains(err.Message, "backend.mutation.invocation_cycle") ||
		!strings.Contains(err.Message, "action:approve -> workflow:fulfillment -> action:approve") {
		t.Fatalf("cycle diagnostic=%q", err.Message)
	}
}

func TestManifestMutationGraphHelpersNormalizeEventsAndFindCycles(t *testing.T) {
	if got := cleanManifestReference(nil); got != "" {
		t.Fatalf("nil reference=%q", got)
	}
	if got := cleanManifestReference("  action "); got != "action" {
		t.Fatalf("normalized reference=%q", got)
	}

	cases := map[string]string{
		"created":  "record:create:order",
		"updated":  "record:update:order",
		"deleted":  "record:delete:order",
		"restored": "record:restore:order",
		"archive":  "record:archive:order",
	}
	for operation, want := range cases {
		if got := manifestRecordEventNode(" order ", operation); got != want {
			t.Fatalf("manifestRecordEventNode(%q)=%q want %q", operation, got, want)
		}
	}

	graph := map[string]map[string]bool{
		"a": {"b": true, "done": true},
		"b": {"a": true},
	}
	want := [][]string{{"a", "b", "a"}}
	if got := manifestDirectedCycles(graph); !reflect.DeepEqual(got, want) {
		t.Fatalf("cycles=%v want %v", got, want)
	}
}
