package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func WorkflowApprovalDueAt(contract definitionmodel.WorkflowApprovalNodeContract, node definitionmodel.WorkflowGraphNode, createdAt time.Time) string {
	if contract.DueSeconds > 0 {
		return createdAt.Add(time.Duration(contract.DueSeconds) * time.Second).Format(time.RFC3339Nano)
	}
	dueAt := strings.TrimSpace(fmt.Sprint(node.Config["due_at"]))
	if dueAt == "<nil>" {
		return ""
	}
	return dueAt
}

func WorkflowCandidateSource(resolvers []definitionmodel.WorkflowAssigneeResolver) string {
	sources := make([]string, 0, len(resolvers))
	for _, resolver := range resolvers {
		sources = append(sources, strings.TrimSpace(resolver.Type))
	}
	return strings.Join(WorkflowUniqueAssignees(sources), ",")
}

func WorkflowOrderedApprovalResolvers(resolvers []definitionmodel.WorkflowAssigneeResolver) []definitionmodel.WorkflowAssigneeResolver {
	ordered := append([]definitionmodel.WorkflowAssigneeResolver(nil), resolvers...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].Priority, ordered[j].Priority
		if left == 0 {
			left = i + 1
		}
		if right == 0 {
			right = j + 1
		}
		return left < right
	})
	return ordered
}

func WorkflowApprovalSubjectUserID(variables map[string]any, employeeField string) string {
	for _, field := range []string{employeeField, "owner", "initiating_user_id"} {
		if userID := WorkflowPayloadString(variables, field); userID != "" {
			return userID
		}
	}
	return ""
}

func WorkflowUniqueAssignees(userIDs []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		unique = append(unique, userID)
	}
	return unique
}

func WorkflowTerminalApprovalOutcome(process workflowmodel.WorkflowProcessInstance, tasks []workflowmodel.WorkflowTask, decided workflowmodel.WorkflowTask) (string, bool) {
	if decided.Decision == "rejected" || decided.Decision == "returned" {
		return decided.Decision, true
	}
	nodeTasks := []workflowmodel.WorkflowTask{}
	for _, task := range tasks {
		if task.NodeID != decided.NodeID {
			continue
		}
		if task.ID == decided.ID {
			task = decided
		}
		nodeTasks = append(nodeTasks, task)
	}
	if len(nodeTasks) == 0 {
		return "", false
	}
	approved := 0
	for _, task := range nodeTasks {
		if task.Status == "approved" {
			approved++
		}
	}
	node, _ := WorkflowGraphNode(process.DefinitionSnapshot.Graph, decided.NodeID)
	mode := strings.TrimSpace(WorkflowApprovalNodeContract(node).Mode)
	if mode == "" {
		mode = "any"
	}
	if mode == "any" {
		return "approved", approved > 0
	}
	return "approved", approved == len(nodeTasks)
}
