package runtimeext

import (
	"context"
	"strings"
)

// Workflow operations available inside the Business Action transaction.
const (
	WorkflowStartOperation    = "start"
	WorkflowWithdrawOperation = "withdraw"
)

// WorkflowGrantDeniedErrorCode is returned when a Handler stages a Workflow
// start its descriptor does not grant.
const WorkflowGrantDeniedErrorCode = "backend.action.workflow_grant_denied"

// WorkflowGrant is the descriptor-declared authority over one Workflow.
type WorkflowGrant struct {
	Key        string
	Operations []string
}

func (g WorkflowGrant) Valid() bool {
	if strings.TrimSpace(g.Key) == "" || len(g.Operations) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, raw := range g.Operations {
		operation := strings.TrimSpace(raw)
		if (operation != WorkflowStartOperation && operation != WorkflowWithdrawOperation) || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}

// WorkflowRouteStep is one authored step of a per-instance approval route.
// Runtime validates and freezes it; the Handler never sees the durable rows.
type WorkflowRouteStep struct {
	StepKey           string
	Title             string
	Mode              string
	RequiredApprovals int
	AssigneeUserIDs   []string
	// Deferred leaves the step's approvers to the previous step's approver (or
	// the initiator) at decision time. It is only accepted when the workflow's
	// approval route allows deferred steps and never on the first step.
	Deferred bool
}

// WorkflowStart is a Workflow start staged inside the Action transaction. It
// commits with the Action's record mutations or not at all.
type WorkflowStart struct {
	WorkflowKey string
	ObjectKey   string
	RecordID    string
	Route       []WorkflowRouteStep
	Variables   map[string]any
}

// WorkflowStartReceipt identifies the staged process before it is committed.
type WorkflowStartReceipt struct {
	ProcessID string
	StepKeys  []string
}

// WorkflowStartExecution is optional so a Runtimeext test double does not
// accidentally acquire Workflow start authority. Generated Action bindings
// expose it only for an authored workflow start grant.
type WorkflowStartExecution interface {
	StageWorkflowStart(context.Context, WorkflowStart) (WorkflowStartReceipt, error)
}

// StageWorkflowStart stages one Workflow start on the current Action
// execution. The Workflow becomes durable only when the Action commits.
func StageWorkflowStart(ctx context.Context, execution ActionExecution, start WorkflowStart) (WorkflowStartReceipt, error) {
	capability, ok := execution.(WorkflowStartExecution)
	if !ok {
		return WorkflowStartReceipt{}, &BusinessError{Code: "backend.action.workflow_start_unavailable", Message: "Runtime Workflow start execution is unavailable"}
	}
	return capability.StageWorkflowStart(ctx, start)
}

// WorkflowWithdrawal binds withdrawal to the exact business record. Runtime
// checks initiator ownership and the process revision at the Action commit.
type WorkflowWithdrawal struct {
	WorkflowKey string
	ObjectKey   string
	RecordID    string
	ProcessID   string
}

// WorkflowWithdrawalReceipt is durable only when the business Action commits.
type WorkflowWithdrawalReceipt struct {
	ProcessID   string
	CommandID   string
	WithdrawnAt string
}

type WorkflowWithdrawalExecution interface {
	StageWorkflowWithdrawal(context.Context, WorkflowWithdrawal) (WorkflowWithdrawalReceipt, error)
}

func StageWorkflowWithdrawal(ctx context.Context, execution ActionExecution, withdrawal WorkflowWithdrawal) (WorkflowWithdrawalReceipt, error) {
	capability, ok := execution.(WorkflowWithdrawalExecution)
	if !ok {
		return WorkflowWithdrawalReceipt{}, &BusinessError{Code: "backend.action.workflow_withdrawal_unavailable", Message: "Runtime Workflow withdrawal execution is unavailable"}
	}
	return capability.StageWorkflowWithdrawal(ctx, withdrawal)
}
