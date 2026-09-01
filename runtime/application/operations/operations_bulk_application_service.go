package operations

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const operationsBulkMaximumItems = 100

type OperationsBulkFilter struct {
	IDs    []string `json:"ids"`
	Status string   `json:"status,omitempty"`
}

type OperationsBulkDryRunRequest struct {
	Owner     string               `json:"owner"`
	Action    string               `json:"action"`
	Filter    OperationsBulkFilter `json:"filter"`
	Limit     int                  `json:"limit"`
	Reason    string               `json:"reason"`
	Reference string               `json:"reference,omitempty"`
}

type OperationsBulkCandidate struct {
	ID       string                   `json:"id"`
	Eligible bool                     `json:"eligible"`
	Reason   string                   `json:"reason,omitempty"`
	Item     OperationsDeadLetterItem `json:"item,omitempty"`
}

type OperationsBulkPlan struct {
	DryRunOperationID string                            `json:"dry_run_operation_id"`
	Owner             string                            `json:"owner"`
	Action            string                            `json:"action"`
	Filter            OperationsBulkFilter              `json:"filter"`
	Limit             int                               `json:"limit"`
	Candidates        []OperationsBulkCandidate         `json:"candidates"`
	ConfirmationToken string                            `json:"confirmation_token"`
	ExpiresAt         time.Time                         `json:"expires_at"`
	Receipt           operationsmodel.OperationsReceipt `json:"receipt"`
}

type OperationsBulkApplyRequest struct {
	DryRunOperationID string `json:"dry_run_operation_id"`
	ConfirmationToken string `json:"confirmation_token"`
	Confirm           bool   `json:"confirm"`
	Reason            string `json:"reason"`
	Reference         string `json:"reference,omitempty"`
}

type OperationsBulkItemResult struct {
	ID        string                   `json:"id"`
	Status    string                   `json:"status"`
	ErrorCode string                   `json:"error_code,omitempty"`
	Item      OperationsDeadLetterItem `json:"item,omitempty"`
}

type OperationsBulkApplyResult struct {
	DryRunOperationID string                            `json:"dry_run_operation_id"`
	Items             []OperationsBulkItemResult        `json:"items"`
	Succeeded         int                               `json:"succeeded"`
	Failed            int                               `json:"failed"`
	Receipt           operationsmodel.OperationsReceipt `json:"receipt"`
}

func (s *OperationsApplicationService) DryRunBulkDeadLetters(ctx context.Context, request OperationsBulkDryRunRequest, key string, principal principalmodel.Principal) (OperationsBulkPlan, error) {
	request.Owner, request.Action, request.Filter.Status = strings.TrimSpace(request.Owner), strings.TrimSpace(request.Action), strings.TrimSpace(request.Filter.Status)
	request.Filter.IDs = normalizedBulkIDs(request.Filter.IDs)
	if request.Limit <= 0 || request.Limit > operationsBulkMaximumItems || len(request.Filter.IDs) == 0 || len(request.Filter.IDs) > request.Limit {
		return OperationsBulkPlan{}, apperror.New(apperror.KindBadRequest, "backend.operations.bulk_filter_invalid", nil, nil)
	}
	if request.Action != OperationsDeadLetterResolve && request.Action != OperationsDeadLetterRetry && request.Action != OperationsDeadLetterAck {
		return OperationsBulkPlan{}, apperror.New(apperror.KindBadRequest, "backend.operations.dead_letter_action_invalid", nil, nil)
	}
	adapter, err := s.deadLetterOwner(request.Owner)
	if err != nil {
		return OperationsBulkPlan{}, err
	}
	receipt, decision, err := s.Submit(ctx, OperationsSubmitRequest{Kind: "bulk_operation.dry_run", Permission: operationsDefinitionPermission("bulk_operation.dry_run"), ResourceType: "bulk_operation", ResourceID: request.Owner + ":" + request.Action, Reason: request.Reason, Reference: request.Reference, Payload: request}, key, principal)
	if err != nil {
		return OperationsBulkPlan{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var plan OperationsBulkPlan
		if json.Unmarshal(receipt.Result, &plan) != nil {
			return OperationsBulkPlan{}, apperror.New(apperror.KindInternal, "backend.operations.bulk_receipt_invalid", nil, nil)
		}
		plan.Receipt = receipt
		return plan, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "prepare bounded bulk operation")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsBulkPlan{}, err
	}
	plan := OperationsBulkPlan{DryRunOperationID: receipt.Command.ID, Owner: request.Owner, Action: request.Action, Filter: request.Filter, Limit: request.Limit, ExpiresAt: s.now().UTC().Add(15 * time.Minute)}
	for _, id := range request.Filter.IDs {
		item, inspectErr := adapter.Inspect(ctx, id, principal)
		candidate := OperationsBulkCandidate{ID: id, Item: item}
		if inspectErr != nil {
			candidate.Reason = "inspect_failed"
		} else if request.Filter.Status != "" && item.Status != request.Filter.Status {
			candidate.Reason = "status_mismatch"
		} else if !containsBulkAction(item.AllowedActions, request.Action) {
			candidate.Reason = "action_not_allowed"
		} else {
			candidate.Eligible = true
		}
		plan.Candidates = append(plan.Candidates, candidate)
	}
	plan.ConfirmationToken, _ = idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "bulk_operation.confirm", ResourceType: "bulk_operation", TargetID: receipt.Command.ID, Payload: map[string]any{"owner": plan.Owner, "action": plan.Action, "filter": plan.Filter, "candidates": eligibleBulkIDs(plan.Candidates), "expires_at": plan.ExpiresAt.Format(time.RFC3339Nano)}})
	result, _ := json.Marshal(plan)
	receipt.Command.Status, receipt.Result, receipt.RelatedIDs = operationsmodel.OperationsStatusSucceeded, result, append([]string(nil), request.Filter.IDs...)
	receipt.NextAction = "review per-item eligibility and apply before expiry with the exact confirmation token"
	receipt, err = s.Finish(ctx, receipt, scope)
	if err != nil {
		return OperationsBulkPlan{}, err
	}
	plan.Receipt = receipt
	return plan, nil
}

func (s *OperationsApplicationService) ApplyBulkDeadLetters(ctx context.Context, request OperationsBulkApplyRequest, key string, principal principalmodel.Principal) (OperationsBulkApplyResult, error) {
	if err := operationsAuthorize(principal, operationsDefinitionPermission("bulk_operation.apply")); err != nil {
		return OperationsBulkApplyResult{}, err
	}
	if !request.Confirm || strings.TrimSpace(request.DryRunOperationID) == "" || strings.TrimSpace(request.ConfirmationToken) == "" {
		return OperationsBulkApplyResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.bulk_confirmation_required", nil, nil)
	}
	dryReceipt, found, err := s.repository.GetOperationsReceipt(ctx, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID}, strings.TrimSpace(request.DryRunOperationID))
	if err != nil || !found || dryReceipt.Command.Kind != "bulk_operation.dry_run" || dryReceipt.Command.Status != operationsmodel.OperationsStatusSucceeded {
		return OperationsBulkApplyResult{}, apperror.New(apperror.KindConflict, "backend.operations.bulk_dry_run_invalid", err, nil)
	}
	var plan OperationsBulkPlan
	if json.Unmarshal(dryReceipt.Result, &plan) != nil || plan.ConfirmationToken != strings.TrimSpace(request.ConfirmationToken) || !plan.ExpiresAt.After(s.now().UTC()) {
		return OperationsBulkApplyResult{}, apperror.New(apperror.KindConflict, "backend.operations.bulk_confirmation_mismatch", nil, nil)
	}
	adapter, err := s.deadLetterOwner(plan.Owner)
	if err != nil {
		return OperationsBulkApplyResult{}, err
	}
	receipt, decision, err := s.Submit(ctx, OperationsSubmitRequest{Kind: "bulk_operation.apply", Permission: operationsDefinitionPermission("bulk_operation.apply"), ResourceType: "bulk_operation", ResourceID: plan.DryRunOperationID, Reason: request.Reason, Reference: request.Reference, Payload: map[string]any{"dry_run_operation_id": plan.DryRunOperationID, "confirmation_token": plan.ConfirmationToken}}, key, principal)
	if err != nil {
		return OperationsBulkApplyResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var result OperationsBulkApplyResult
		if json.Unmarshal(receipt.Result, &result) != nil {
			return OperationsBulkApplyResult{}, apperror.New(apperror.KindInternal, "backend.operations.bulk_receipt_invalid", nil, nil)
		}
		result.Receipt = receipt
		return result, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "apply bounded bulk operation")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsBulkApplyResult{}, err
	}
	result := OperationsBulkApplyResult{DryRunOperationID: plan.DryRunOperationID}
	for _, candidate := range plan.Candidates {
		if !candidate.Eligible {
			continue
		}
		item, actionErr := adapter.Act(ctx, candidate.ID, plan.Action, strings.TrimSpace(request.Reason), receipt.Command.ID+":"+candidate.ID, principal)
		itemResult := OperationsBulkItemResult{ID: candidate.ID, Item: item, Status: "succeeded"}
		if actionErr != nil {
			itemResult.Status, itemResult.ErrorCode = "failed", "backend.operations.dead_letter_owner_rejected"
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Items = append(result.Items, itemResult)
	}
	encoded, _ := json.Marshal(result)
	receipt.Command.Status, receipt.Result = operationsmodel.OperationsStatusSucceeded, encoded
	if result.Failed > 0 {
		receipt.NextAction = "inspect failed item results; rerun a new dry-run after owner state is corrected"
	} else {
		receipt.NextAction = "verify owner evidence for every succeeded item"
	}
	receipt, err = s.Finish(ctx, receipt, scope)
	if err != nil {
		return OperationsBulkApplyResult{}, err
	}
	result.Receipt = receipt
	return result, nil
}

func normalizedBulkIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}
func containsBulkAction(actions []string, action string) bool {
	for _, candidate := range actions {
		if strings.TrimSpace(candidate) == action {
			return true
		}
	}
	return false
}
func eligibleBulkIDs(candidates []OperationsBulkCandidate) []string {
	ids := []string{}
	for _, candidate := range candidates {
		if candidate.Eligible {
			ids = append(ids, candidate.ID)
		}
	}
	return ids
}
