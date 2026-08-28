package policy

import (
	"fmt"
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

// OperationsNormalizeCommand removes representation-only whitespace before a
// command is authorized, fingerprinted or persisted.
func OperationsNormalizeCommand(command operationsmodel.OperationsCommand) operationsmodel.OperationsCommand {
	command.ID = strings.TrimSpace(command.ID)
	command.Kind = strings.TrimSpace(command.Kind)
	command.Permission = strings.TrimSpace(command.Permission)
	command.Scope.WorkspaceID = strings.TrimSpace(command.Scope.WorkspaceID)
	command.Scope.SystemPurpose = strings.TrimSpace(command.Scope.SystemPurpose)
	command.Scope.ResourceType = strings.TrimSpace(command.Scope.ResourceType)
	command.Scope.ResourceID = strings.TrimSpace(command.Scope.ResourceID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.RequestFingerprint = strings.TrimSpace(command.RequestFingerprint)
	command.RequestedBy = strings.TrimSpace(command.RequestedBy)
	command.Reason = strings.TrimSpace(command.Reason)
	command.Reference = strings.TrimSpace(command.Reference)
	return command
}

// OperationsValidateCommand enforces the cross-owner command envelope. Each
// business owner remains responsible for its permission and precondition
// decision before the command is registered.
func OperationsValidateCommand(command operationsmodel.OperationsCommand) error {
	command = OperationsNormalizeCommand(command)
	if command.ID == "" || command.Kind == "" || command.Permission == "" {
		return fmt.Errorf("operations.command_identity_required")
	}
	if command.IdempotencyKey == "" || command.RequestFingerprint == "" {
		return fmt.Errorf("operations.idempotency_contract_required")
	}
	if command.RequestedBy == "" || command.Reason == "" {
		return fmt.Errorf("operations.audit_context_required")
	}
	if command.Scope.ResourceType == "" {
		return fmt.Errorf("operations.resource_scope_required")
	}
	workspaceScoped := command.Scope.WorkspaceID != ""
	systemScoped := command.Scope.SystemPurpose != ""
	if workspaceScoped == systemScoped {
		return fmt.Errorf("operations.execution_scope_required")
	}
	if command.Status != operationsmodel.OperationsStatusCreated {
		return fmt.Errorf("operations.initial_status_invalid")
	}
	if command.CreatedAt.IsZero() || command.UpdatedAt.IsZero() || !command.UpdatedAt.Equal(command.CreatedAt) || command.StartedAt != nil || command.FinishedAt != nil {
		return fmt.Errorf("operations.initial_timestamps_invalid")
	}
	return nil
}

// OperationsClassifySubmission guarantees that a repeated key replays the
// original operation while a changed semantic payload is rejected.
func OperationsClassifySubmission(existing *operationsmodel.OperationsReceipt, incoming operationsmodel.OperationsCommand) operationsmodel.OperationsSubmissionDecision {
	if existing == nil {
		return operationsmodel.OperationsSubmissionAccepted
	}
	existingCommand := OperationsNormalizeCommand(existing.Command)
	incoming = OperationsNormalizeCommand(incoming)
	if existingCommand.IdempotencyKey == incoming.IdempotencyKey && existingCommand.RequestFingerprint == incoming.RequestFingerprint {
		return operationsmodel.OperationsSubmissionReplay
	}
	return operationsmodel.OperationsSubmissionConflict
}

// OperationsValidateReceipt keeps terminal failure recovery explicit and
// prevents a successful receipt from carrying contradictory failure facts.
func OperationsValidateReceipt(receipt operationsmodel.OperationsReceipt) error {
	command := OperationsNormalizeCommand(receipt.Command)
	if strings.TrimSpace(receipt.StatusURL) == "" {
		return fmt.Errorf("operations.status_url_required")
	}
	switch command.Status {
	case operationsmodel.OperationsStatusCreated:
		if command.StartedAt != nil || command.FinishedAt != nil {
			return fmt.Errorf("operations.created_timestamps_invalid")
		}
	case operationsmodel.OperationsStatusStarted:
		if command.StartedAt == nil || command.FinishedAt != nil {
			return fmt.Errorf("operations.started_timestamps_invalid")
		}
	case operationsmodel.OperationsStatusSucceeded:
		if command.StartedAt == nil || command.FinishedAt == nil || receipt.FailureClass != "" || strings.TrimSpace(receipt.ErrorCode) != "" {
			return fmt.Errorf("operations.success_receipt_invalid")
		}
	case operationsmodel.OperationsStatusFailed:
		if command.StartedAt == nil || command.FinishedAt == nil || strings.TrimSpace(receipt.ErrorCode) == "" || !operationsFailureClassValid(receipt.FailureClass) {
			return fmt.Errorf("operations.failure_receipt_invalid")
		}
	default:
		return fmt.Errorf("operations.status_invalid")
	}
	return nil
}

func operationsFailureClassValid(class operationsmodel.OperationsFailureClass) bool {
	switch class {
	case operationsmodel.OperationsFailureRetryable, operationsmodel.OperationsFailureTerminal, operationsmodel.OperationsFailureManualIntervention:
		return true
	default:
		return false
	}
}
