package policy

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowGraphPublishesStrictWaitAndTimerNodeContracts(t *testing.T) {
	valid := []definitionmodel.WorkflowGraphNode{
		{ID: "until", Type: "wait_until", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{At: "2026-07-21T08:00:00Z", Timezone: "UTC"}}},
		{ID: "duration", Type: "wait_duration", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{DurationSeconds: 30, Timezone: "UTC"}}},
		{ID: "calendar", Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{TimerKey: "deadline", Purpose: "resume", SourceField: "due_at", BusinessCalendarKey: "weekday", Timezone: "Asia/Shanghai"}}},
	}
	for _, node := range valid {
		graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, node}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: node.ID}}}
		if err := WorkflowValidateGraph(graph); err != nil {
			t.Fatalf("valid %s node rejected: %v", node.Type, err)
		}
	}
	invalid := []definitionmodel.WorkflowGraphNode{
		{ID: "until", Type: "wait_until", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{At: "not-time"}}},
		{ID: "duration", Type: "wait_duration", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{DurationSeconds: 0}}},
		{ID: "timer", Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{DurationSeconds: 30}}},
		{ID: "ambiguous", Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{TimerKey: "x", Purpose: "x", At: "2026-07-21T08:00:00Z", SourceField: "due_at"}}},
	}
	for _, node := range invalid {
		graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, node}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: node.ID}}}
		if err := WorkflowValidateGraph(graph); apperror.CodeOf(err) != "backend.workflow.timer_contract_invalid" {
			t.Fatalf("invalid %s contract error = %v", node.ID, err)
		}
	}
}
