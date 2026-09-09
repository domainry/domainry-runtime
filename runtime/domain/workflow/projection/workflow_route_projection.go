package projection

import (
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// WorkflowRouteStepView is the participant-facing projection of one route
// step. Assignee identities are disclosed only where the reader already has a
// legitimate reason to see them; every other reader sees the counts.
type WorkflowRouteStepView struct {
	StepNo            int                                   `json:"step_no"`
	StepKey           string                                `json:"step_key"`
	Title             string                                `json:"title,omitempty"`
	Status            string                                `json:"status"`
	Mode              string                                `json:"mode"`
	RequiredApprovals int                                   `json:"required_approvals"`
	ApprovedCount     int                                   `json:"approved_count"`
	AssigneeCount     int                                   `json:"assignee_count"`
	ConfigurableByMe  bool                                  `json:"configurable_by_me"`
	ConfiguredBy      string                                `json:"configured_by,omitempty"`
	ConfiguredAt      string                                `json:"configured_at,omitempty"`
	ConfigureSource   string                                `json:"configure_source,omitempty"`
	Assignees         []workflowmodel.WorkflowRouteAssignee `json:"assignees,omitempty"`
}

// WorkflowRouteView is the complete per-instance approval route as one
// participant may see it.
type WorkflowRouteView struct {
	ProcessID            string                  `json:"process_id"`
	NodeID               string                  `json:"node_id,omitempty"`
	Steps                []WorkflowRouteStepView `json:"steps"`
	PendingConfiguration []string                `json:"pending_configuration"`
	ConfigurableByMe     bool                    `json:"configurable_by_me"`
}

// WorkflowRouteVisibleTo reports whether the principal may read the route at
// all: its initiator and every assignee named on any step.
func WorkflowRouteVisibleTo(process workflowmodel.WorkflowProcessInstance, steps []workflowmodel.WorkflowRouteStep, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	if strings.TrimSpace(process.InitiatorID) == userID {
		return true
	}
	for _, step := range steps {
		for _, assignee := range step.AssigneeSnapshot {
			if strings.TrimSpace(assignee.UserID) == userID {
				return true
			}
		}
	}
	return false
}

// WorkflowRouteForPrincipal projects the durable route rows for one reader.
// Approved counts come from the durable tasks of each step's node instance, so
// the view never recomputes a threshold the engine already owns.
func WorkflowRouteForPrincipal(process workflowmodel.WorkflowProcessInstance, steps []workflowmodel.WorkflowRouteStep, tasks []workflowmodel.WorkflowTask, userID string) WorkflowRouteView {
	userID = strings.TrimSpace(userID)
	initiator := strings.TrimSpace(process.InitiatorID) == userID
	ordered := append([]workflowmodel.WorkflowRouteStep(nil), steps...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StepNo < ordered[j].StepNo })
	route, _ := workflowRouteContract(process, ordered)
	view := WorkflowRouteView{ProcessID: process.ID, Steps: make([]WorkflowRouteStepView, 0, len(ordered)), PendingConfiguration: []string{}}
	approvals := map[string]int{}
	for _, task := range tasks {
		if task.Status == "approved" {
			approvals[task.NodeInstanceID]++
		}
	}
	for index, step := range ordered {
		if view.NodeID == "" {
			view.NodeID = step.NodeID
		}
		assignee := workflowRouteStepHasAssignee(step, userID)
		configurable := step.Status == "configurable"
		configurableByMe := configurable && workflowRouteConfigurerAllows(route, ordered, index, process, userID)
		item := WorkflowRouteStepView{
			StepNo: step.StepNo, StepKey: step.StepKey, Title: step.Title, Status: step.Status,
			Mode: workflowpolicy.WorkflowRouteStepMode(step.Mode), RequiredApprovals: step.RequiredApprovals,
			ApprovedCount: approvals[step.NodeInstanceID], AssigneeCount: len(step.AssigneeSnapshot),
			ConfigurableByMe: configurableByMe, ConfiguredBy: step.ConfiguredBy, ConfiguredAt: step.ConfiguredAt, ConfigureSource: step.ConfigureSource,
		}
		if initiator || assignee || workflowRouteStepCompleted(step) {
			item.Assignees = append([]workflowmodel.WorkflowRouteAssignee(nil), step.AssigneeSnapshot...)
		}
		if configurable {
			view.PendingConfiguration = append(view.PendingConfiguration, step.StepKey)
		}
		if configurableByMe {
			view.ConfigurableByMe = true
		}
		view.Steps = append(view.Steps, item)
	}
	return view
}

func workflowRouteStepCompleted(step workflowmodel.WorkflowRouteStep) bool {
	switch step.Status {
	case "approved", "rejected", "returned", "skipped":
		return true
	default:
		return false
	}
}

func workflowRouteStepHasAssignee(step workflowmodel.WorkflowRouteStep, userID string) bool {
	for _, assignee := range step.AssigneeSnapshot {
		if strings.TrimSpace(assignee.UserID) == userID {
			return true
		}
	}
	return false
}

// WorkflowRouteConfigurerAllows reports whether userID may configure the step
// at the given index under the route's deferred-configurer policy.
func WorkflowRouteConfigurerAllows(route definitionmodel.WorkflowApprovalRouteContract, steps []workflowmodel.WorkflowRouteStep, index int, process workflowmodel.WorkflowProcessInstance, userID string) bool {
	return workflowRouteConfigurerAllows(route, steps, index, process, userID)
}

func workflowRouteConfigurerAllows(route definitionmodel.WorkflowApprovalRouteContract, steps []workflowmodel.WorkflowRouteStep, index int, process workflowmodel.WorkflowProcessInstance, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	if route.DeferredConfigurer == "initiator" {
		return strings.TrimSpace(process.InitiatorID) == userID
	}
	if index <= 0 || index > len(steps)-1 {
		return false
	}
	return workflowRouteStepHasAssignee(steps[index-1], userID)
}

func workflowRouteContract(process workflowmodel.WorkflowProcessInstance, steps []workflowmodel.WorkflowRouteStep) (definitionmodel.WorkflowApprovalRouteContract, bool) {
	nodeID := ""
	if len(steps) > 0 {
		nodeID = steps[0].NodeID
	}
	node, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, nodeID)
	if !found {
		return workflowpolicy.WorkflowApprovalRouteDefaults(definitionmodel.WorkflowApprovalRouteContract{}), false
	}
	route, ok := workflowpolicy.WorkflowApprovalRoute(node)
	if !ok {
		return workflowpolicy.WorkflowApprovalRouteDefaults(definitionmodel.WorkflowApprovalRouteContract{}), false
	}
	return route, true
}

// WorkflowRouteContract returns the effective route contract of the node the
// durable steps belong to.
func WorkflowRouteContract(process workflowmodel.WorkflowProcessInstance, steps []workflowmodel.WorkflowRouteStep) (definitionmodel.WorkflowApprovalRouteContract, bool) {
	return workflowRouteContract(process, steps)
}
