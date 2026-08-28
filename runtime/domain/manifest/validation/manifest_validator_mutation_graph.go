package validation

import (
	"fmt"
	"sort"
	"strings"
)

// validateMutationGraph closes publication-time gaps that cannot be repaired
// safely once an Action has started executing.
func (state *validationState) validateMutationGraph() {
	graph := map[string]map[string]bool{}
	paths := map[string]string{}
	addEdge := func(from, to, path string) {
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if graph[from] == nil {
			graph[from] = map[string]bool{}
		}
		graph[from][to] = true
		if paths[from] == "" {
			paths[from] = path
		}
	}

	for index, rule := range state.manifest.AutomationRules {
		if strings.TrimSpace(rule.Trigger.Phase) != "after" {
			continue
		}
		ruleNode := "automation:" + strings.TrimSpace(rule.Key)
		addEdge(manifestRecordEventNode(rule.ObjectKey, rule.Trigger.Operation), ruleNode, fmt.Sprintf("automation_rules[%d].trigger", index))
		for instructionIndex, instruction := range rule.Instructions {
			path := fmt.Sprintf("automation_rules[%d].instructions[%d]", index, instructionIndex)
			switch strings.TrimSpace(instruction.Type) {
			case "invoke_business_action":
				addEdge(ruleNode, "action:"+cleanManifestReference(instruction.Config["action_key"]), path+".config.action_key")
			case "start_workflow":
				addEdge(ruleNode, "workflow:"+cleanManifestReference(instruction.Config["workflow_key"]), path+".config.workflow_key")
			}
		}
	}

	for index, workflow := range state.manifest.Workflows {
		workflowNode := "workflow:" + strings.TrimSpace(workflow.Key)
		path := fmt.Sprintf("workflows[%d]", index)
		if trigger := workflow.TriggerContract; trigger != nil {
			switch strings.TrimSpace(trigger.Type) {
			case "action_completed", "action_executed":
				actionKey := strings.TrimSpace(trigger.Event)
				if actionKey != "" {
					addEdge("action:"+actionKey, workflowNode, path+".trigger_contract.event")
				}
			case "record_created", "record_updated", "record_deleted", "record_restored", "record_event":
				for _, objectKey := range append([]string{trigger.ObjectKey}, trigger.ObjectKeys...) {
					operation := strings.TrimPrefix(strings.TrimSpace(trigger.Type), "record_")
					operation = strings.TrimSuffix(operation, "d")
					if trigger.Type == "record_event" {
						operation = strings.TrimSpace(trigger.Event)
					}
					addEdge(manifestRecordEventNode(objectKey, operation), workflowNode, path+".trigger_contract.object_key")
				}
			}
		}
		if workflow.Graph == nil {
			continue
		}
		for nodeIndex, node := range workflow.Graph.Nodes {
			nodePath := fmt.Sprintf("%s.graph.nodes[%d]", path, nodeIndex)
			if node.Contract != nil {
				if node.Contract.Action != nil {
					addEdge(workflowNode, "action:"+strings.TrimSpace(node.Contract.Action.ActionKey), nodePath+".contract.action.action_key")
				}
				if node.Contract.Approval != nil {
					addEdge(workflowNode, "action:"+strings.TrimSpace(node.Contract.Approval.ReminderActionKey), nodePath+".contract.approval.reminder_action_key")
				}
				if node.Contract.CC != nil {
					addEdge(workflowNode, "action:"+strings.TrimSpace(node.Contract.CC.NotificationActionKey), nodePath+".contract.cc.notification_action_key")
				}
			}
		}
	}

	for _, cycle := range manifestDirectedCycles(graph) {
		path := paths[cycle[0]]
		state.add(path, "backend.mutation.invocation_cycle: %s", strings.Join(cycle, " -> "))
	}
}

func cleanManifestReference(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func manifestRecordEventNode(objectKey, operation string) string {
	operation = strings.TrimSpace(operation)
	switch operation {
	case "created":
		operation = "create"
	case "updated":
		operation = "update"
	case "deleted":
		operation = "delete"
	case "restored":
		operation = "restore"
	}
	return "record:" + operation + ":" + strings.TrimSpace(objectKey)
}

func manifestDirectedCycles(graph map[string]map[string]bool) [][]string {
	state, stack, position := map[string]uint8{}, []string{}, map[string]int{}
	cycles := [][]string{}
	var visit func(string)
	visit = func(node string) {
		state[node] = 1
		position[node] = len(stack)
		stack = append(stack, node)
		next := make([]string, 0, len(graph[node]))
		for target := range graph[node] {
			next = append(next, target)
		}
		sort.Strings(next)
		for _, target := range next {
			if state[target] == 0 {
				visit(target)
			} else if state[target] == 1 {
				cycle := append([]string(nil), stack[position[target]:]...)
				cycle = append(cycle, target)
				cycles = append(cycles, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		delete(position, node)
		state[node] = 2
	}
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if state[node] == 0 {
			visit(node)
		}
	}
	return cycles
}
