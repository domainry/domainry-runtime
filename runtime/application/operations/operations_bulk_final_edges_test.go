package operations

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsBulkFinalFilterActionAndReplayConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 5, 6, 7, 0, time.UTC)
	principal := operationsAdminPrincipal()
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", Status: "dead", AllowedActions: []string{"retry", "resolve", "ack"}}}}
	service := newBulkEdgeService(t, &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, owner, now, "filter")
	if _, err := service.DryRunBulkDeadLetters(t.Context(), OperationsBulkDryRunRequest{Owner: "owner", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: 0}, "zero", principal); apperror.CodeOf(err) != "backend.operations.bulk_filter_invalid" {
		t.Fatalf("zero limit error = %v", err)
	}
	for _, action := range []string{OperationsDeadLetterResolve, OperationsDeadLetterAck} {
		if _, err := service.DryRunBulkDeadLetters(t.Context(), OperationsBulkDryRunRequest{Owner: "owner", Action: action, Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: 1, Reason: "test"}, action, principal); err != nil {
			t.Fatalf("action %q error = %v", action, err)
		}
	}
	if got := normalizedBulkIDs([]string{" ", "a"}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("normalized ids = %#v", got)
	}

	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service = newBulkEdgeService(t, ledger, owner, now, "replay")
	request := OperationsBulkDryRunRequest{Owner: "owner", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: 1, Reason: "test"}
	first, err := service.DryRunBulkDeadLetters(t.Context(), request, "dry", principal)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.DryRunBulkDeadLetters(t.Context(), request, "dry", principal)
	if err != nil || replayed.DryRunOperationID != first.DryRunOperationID {
		t.Fatalf("dry replay=%#v err=%v", replayed, err)
	}
	current := owner.items["a"]
	current.AllowedActions = []string{"resolve"}
	owner.items["a"] = current
	if _, err := service.DryRunBulkDeadLetters(t.Context(), request, "dry", principal); apperror.CodeOf(err) != "backend.operations.bulk_owner_state_changed" {
		t.Fatalf("changed owner replay error = %v", err)
	}
	current.AllowedActions = []string{"retry", "resolve", "ack"}
	owner.items["a"] = current
	replaceOperationsReceiptKind(ledger, "bulk_operation.dry_run", func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusFailed
	})
	if _, err := service.DryRunBulkDeadLetters(t.Context(), request, "dry", principal); apperror.CodeOf(err) != "backend.operations.transition_conflict" {
		t.Fatalf("non-succeeded dry replay error = %v", err)
	}
}

func TestOperationsBulkApplyFinalValidationAndSubmissionConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 6, 7, 8, 0, time.UTC)
	principal := operationsAdminPrincipal()
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", Status: "dead", AllowedActions: []string{"retry"}}}}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := newBulkEdgeService(t, ledger, owner, now, "apply-edge")
	dryRequest := OperationsBulkDryRunRequest{Owner: "owner", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: 1, Reason: "test"}
	plan, err := service.DryRunBulkDeadLetters(t.Context(), dryRequest, "dry", principal)
	if err != nil {
		t.Fatal(err)
	}
	for name, request := range map[string]OperationsBulkApplyRequest{
		"id":    {Confirm: true, ConfirmationToken: "token"},
		"token": {Confirm: true, DryRunOperationID: plan.DryRunOperationID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ApplyBulkDeadLetters(t.Context(), request, "invalid", principal); apperror.CodeOf(err) != "backend.operations.bulk_confirmation_required" {
				t.Fatalf("confirmation error = %v", err)
			}
		})
	}
	valid := OperationsBulkApplyRequest{Confirm: true, DryRunOperationID: plan.DryRunOperationID, ConfirmationToken: plan.ConfirmationToken, Reason: "test"}
	if _, err := service.ApplyBulkDeadLetters(t.Context(), OperationsBulkApplyRequest{Confirm: true, DryRunOperationID: "missing", ConfirmationToken: "token"}, "missing", principal); apperror.CodeOf(err) != "backend.operations.bulk_dry_run_invalid" {
		t.Fatalf("missing dry-run error = %v", err)
	}

	var original operationsmodel.OperationsReceipt
	for _, receipt := range ledger.receipts {
		if receipt.Command.Kind == "bulk_operation.dry_run" {
			original = receipt
		}
	}
	mutations := []func(*operationsmodel.OperationsReceipt){
		func(receipt *operationsmodel.OperationsReceipt) { receipt.Command.Kind = "wrong" },
		func(receipt *operationsmodel.OperationsReceipt) {
			receipt.Command.Status = operationsmodel.OperationsStatusFailed
		},
		func(receipt *operationsmodel.OperationsReceipt) {
			var stored OperationsBulkPlan
			_ = json.Unmarshal(receipt.Result, &stored)
			stored.ExpiresAt = now
			receipt.Result, _ = json.Marshal(stored)
		},
	}
	for index, mutate := range mutations {
		for key, receipt := range ledger.receipts {
			if receipt.Command.ID == original.Command.ID {
				mutated := original
				mutate(&mutated)
				ledger.receipts[key] = mutated
			}
		}
		if _, err := service.ApplyBulkDeadLetters(t.Context(), valid, "mutated", principal); err == nil {
			t.Fatalf("mutated dry-run %d accepted", index)
		}
	}
	for key, receipt := range ledger.receipts {
		if receipt.Command.ID == original.Command.ID {
			ledger.receipts[key] = original
		}
	}

	ledger.registerErr = errors.New("apply register failed")
	if _, err := service.ApplyBulkDeadLetters(t.Context(), valid, "register", principal); apperror.CodeOf(err) != "backend.operations.register_failed" {
		t.Fatalf("apply submit error = %v", err)
	}
	ledger.registerErr = nil
	result, err := service.ApplyBulkDeadLetters(t.Context(), valid, "apply", principal)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ApplyBulkDeadLetters(t.Context(), valid, "apply", principal)
	if err != nil || replayed.DryRunOperationID != result.DryRunOperationID {
		t.Fatalf("apply replay=%#v err=%v", replayed, err)
	}
	replaceOperationsReceiptKind(ledger, "bulk_operation.apply", func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusFailed
	})
	if _, err := service.ApplyBulkDeadLetters(t.Context(), valid, "apply", principal); apperror.CodeOf(err) != "backend.operations.transition_conflict" {
		t.Fatalf("non-succeeded apply replay error = %v", err)
	}
}
