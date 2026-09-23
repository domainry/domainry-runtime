package validation

import (
	"errors"
	"github.com/domainry/domainry-foundation/apperror"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"strings"
)

func WorkflowValidationIssueFromError(err error, code, diagnostic string) workflowmodel.WorkflowValidationIssue {
	params := workflowErrorParams(err)
	nodeID, edgeID := strings.TrimSpace(params["node"]), strings.TrimSpace(params["edge"])
	return workflowmodel.WorkflowValidationIssue{Severity: "error", Code: code, MessageKey: code, Message: code, FieldPath: workflowValidationFieldPath(code, nodeID, edgeID), NodeID: nodeID, EdgeID: edgeID, Params: params, Diagnostic: diagnostic}
}

func WorkflowReferenceValidationIssue(code, nodeID, reference string) workflowmodel.WorkflowValidationIssue {
	params := map[string]string{"reference": reference}
	return workflowmodel.WorkflowValidationIssue{Severity: "error", Code: code, MessageKey: code, Message: code, FieldPath: workflowValidationFieldPath(code, nodeID, ""), NodeID: nodeID, Params: params, Diagnostic: "reference=" + reference}
}

func workflowErrorParams(err error) map[string]string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorParams()
	}
	return nil
}

func workflowValidationFieldPath(code, nodeID, edgeID string) string {
	if edgeID != "" {
		return "graph.edges[" + edgeID + "]"
	}
	if nodeID == "" {
		switch {
		case strings.Contains(code, "trigger"):
			return "trigger_contract"
		case strings.Contains(code, "condition_contract"):
			return "condition_contract"
		default:
			return "graph"
		}
	}
	prefix := "graph.nodes[" + nodeID + "]"
	switch {
	case strings.Contains(code, "required_approvals"):
		return prefix + ".contract.approval.required_approvals"
	case strings.Contains(code, "resolver"):
		return prefix + ".contract.approval.resolvers"
	case strings.Contains(code, "approval_mode"):
		return prefix + ".contract.approval.mode"
	case strings.Contains(code, "empty_policy"):
		return prefix + ".contract.approval.empty_assignee_policy"
	case strings.Contains(code, "deadline"):
		return prefix + ".contract.approval.due_seconds"
	case strings.Contains(code, "action_key"):
		return prefix + ".contract.action.action_key"
	case strings.Contains(code, "error_policy") || strings.Contains(code, "error_branch"):
		return prefix + ".contract.action.on_error"
	case strings.Contains(code, "condition"):
		return prefix + ".contract.condition"
	case strings.Contains(code, "branch"):
		return prefix + ".outgoing_edges"
	default:
		return prefix
	}
}
