package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func newBulkEdgeService(t *testing.T, ledger *operationsRepositoryProbe, owner *bulkDeadLetterOwnerProbe, now time.Time, newID string) *OperationsApplicationService {
	t.Helper()
	sequence := 0
	service := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string {
		sequence++
		return fmt.Sprintf("%s-%d", newID, sequence)
	})
	if owner != nil {
		if err := service.RegisterDeadLetterOwner("owner", owner); err != nil {
			t.Fatal(err)
		}
	}
	return service
}

func bulkEdgeRequest() OperationsBulkDryRunRequest {
	return OperationsBulkDryRunRequest{Owner: " owner ", Action: " retry ", Filter: OperationsBulkFilter{IDs: []string{" c ", "a", "b", "a"}, Status: "dead"}, Limit: 3, Reason: "recover"}
}

func TestOperationsBulkDryRunCandidateReplayAndTransitionEdges(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	principal := operationsAdminPrincipal()
	owner := &bulkDeadLetterOwnerProbe{
		items: map[string]OperationsDeadLetterItem{
			"a": {ID: "a", Status: "dead", AllowedActions: []string{"ack"}},
			"b": {ID: "b", Status: "dead", AllowedActions: []string{" retry "}},
		},
		inspectErr: map[string]error{"c": errors.New("inspect failed")},
	}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := newBulkEdgeService(t, ledger, owner, now, "bulk-candidates")
	plan, err := service.DryRunBulkDeadLetters(t.Context(), bulkEdgeRequest(), "dry", principal)
	if err != nil || len(plan.Candidates) != 3 || plan.Candidates[0].Eligible || !plan.Candidates[1].Eligible || plan.Candidates[2].Reason != "inspect_failed" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	if plan.Candidates[0].Reason != "action_not_allowed" {
		t.Fatalf("candidate=%#v", plan.Candidates[0])
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.DryRunBulkDeadLetters(t.Context(), bulkEdgeRequest(), "dry", principal); apperror.CodeOf(err) != "backend.operations.bulk_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}

	invalidAction := bulkEdgeRequest()
	invalidAction.Action = "delete"
	if _, err := service.DryRunBulkDeadLetters(t.Context(), invalidAction, "invalid", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_invalid" {
		t.Fatalf("action err=%v", err)
	}
	missingOwner := bulkEdgeRequest()
	missingOwner.Owner = "missing"
	if _, err := service.DryRunBulkDeadLetters(t.Context(), missingOwner, "missing", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_not_registered" {
		t.Fatalf("owner err=%v", err)
	}

	for _, test := range []struct {
		name   string
		failAt int
		code   string
	}{
		{"authorization", 0, "backend.workspace_scope_required"},
		{"start", 1, "backend.operations.transition_failed"},
		{"finish", 2, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: base, failAt: test.failAt}
			sequence := 0
			service := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string {
				sequence++
				return fmt.Sprintf("%s-%d", test.name, sequence)
			})
			_ = service.RegisterDeadLetterOwner("owner", owner)
			testPrincipal := principal
			if test.name == "authorization" {
				testPrincipal.WorkspaceID = ""
			}
			if _, err := service.DryRunBulkDeadLetters(t.Context(), bulkEdgeRequest(), "key", testPrincipal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOperationsBulkApplyValidationOwnerFailureAndReplayEdges(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	principal := operationsAdminPrincipal()
	owner := &bulkDeadLetterOwnerProbe{
		items: map[string]OperationsDeadLetterItem{
			"a": {ID: "a", Status: "dead", AllowedActions: []string{"retry"}},
			"b": {ID: "b", Status: "dead", AllowedActions: []string{"retry"}},
		},
		actErr: map[string]error{"b": errors.New("owner failed")},
	}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := newBulkEdgeService(t, ledger, owner, now, "bulk-apply")
	dryRequest := OperationsBulkDryRunRequest{Owner: "owner", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a", "b"}}, Limit: 2, Reason: "recover"}
	plan, err := service.DryRunBulkDeadLetters(t.Context(), dryRequest, "dry", principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyBulkDeadLetters(t.Context(), OperationsBulkApplyRequest{}, "apply", principal); apperror.CodeOf(err) != "backend.operations.bulk_confirmation_required" {
		t.Fatalf("confirmation err=%v", err)
	}
	request := OperationsBulkApplyRequest{DryRunOperationID: plan.DryRunOperationID, ConfirmationToken: plan.ConfirmationToken, Confirm: true, Reason: "recover"}
	result, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply", principal)
	if err != nil || result.Succeeded != 1 || result.Failed != 1 || result.Receipt.NextAction == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	replaceOperationsReceiptKind(ledger, "bulk_operation.apply", func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply", principal); apperror.CodeOf(err) != "backend.operations.bulk_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}

	badLedger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}, getErr: errors.New("read failed")}
	badService := newBulkEdgeService(t, badLedger, owner, now, "read-error")
	if _, err := badService.ApplyBulkDeadLetters(t.Context(), request, "apply", principal); apperror.CodeOf(err) != "backend.operations.bulk_dry_run_invalid" {
		t.Fatalf("read err=%v", err)
	}
	if _, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
}

func replaceOperationsReceiptKind(repository *operationsRepositoryProbe, kind string, mutate func(*operationsmodel.OperationsReceipt)) {
	for key, receipt := range repository.receipts {
		if receipt.Command.Kind == kind {
			mutate(&receipt)
			repository.receipts[key] = receipt
			return
		}
	}
}

func TestOperationsBulkApplyMalformedPlanMissingOwnerAndTransitionFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	principal := operationsAdminPrincipal()
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", Status: "dead", AllowedActions: []string{"retry"}}}}
	for _, test := range []struct {
		name       string
		failAt     int
		mutatePlan func(*OperationsBulkPlan)
		code       string
	}{
		{"malformed", 0, nil, "backend.operations.bulk_confirmation_mismatch"},
		{"missing-owner", 0, func(plan *OperationsBulkPlan) { plan.Owner = "missing" }, "backend.operations.dead_letter_owner_not_registered"},
		{"start", 3, func(*OperationsBulkPlan) {}, "backend.operations.transition_failed"},
		{"finish", 4, func(*OperationsBulkPlan) {}, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: base, failAt: test.failAt}
			sequence := 0
			service := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string {
				sequence++
				return fmt.Sprintf("%s-%d", test.name, sequence)
			})
			_ = service.RegisterDeadLetterOwner("owner", owner)
			plan, err := service.DryRunBulkDeadLetters(t.Context(), OperationsBulkDryRunRequest{Owner: "owner", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: 1, Reason: "recover"}, "dry", principal)
			if err != nil {
				t.Fatal(err)
			}
			request := OperationsBulkApplyRequest{DryRunOperationID: plan.DryRunOperationID, ConfirmationToken: plan.ConfirmationToken, Confirm: true, Reason: "recover"}
			replaceOperationsReceipt(base, func(receipt *operationsmodel.OperationsReceipt) {
				if receipt.Command.Kind != "bulk_operation.dry_run" {
					return
				}
				if test.mutatePlan == nil {
					receipt.Result = []byte("{")
					return
				}
				var stored OperationsBulkPlan
				_ = json.Unmarshal(receipt.Result, &stored)
				test.mutatePlan(&stored)
				receipt.Result, _ = json.Marshal(stored)
			})
			if _, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply", principal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOperationsBreakGlassRegistrationValidationAndRepositoryFailures(t *testing.T) {
	var nilService *OperationsApplicationService
	if err := nilService.RegisterBreakGlass(&breakGlassRepositoryProbe{}, &breakGlassAlertProbe{}); apperror.CodeOf(err) != "backend.operations.break_glass_registration_invalid" {
		t.Fatalf("nil registration err=%v", err)
	}
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "break-edge" })
	if err := service.RegisterBreakGlass(nil, &breakGlassAlertProbe{}); apperror.CodeOf(err) != "backend.operations.break_glass_registration_invalid" {
		t.Fatalf("repository err=%v", err)
	}
	if _, err := service.EnableBreakGlass(t.Context(), OperationsBreakGlassEnableCommand{}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("unavailable err=%v", err)
	}
	repository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}, createErr: errBreakGlassProbe}
	_ = service.RegisterBreakGlass(repository, &breakGlassAlertProbe{})
	command := OperationsBreakGlassEnableCommand{DurationSeconds: 60, ApproverIDs: []string{"a", "b"}, Reason: "incident", IncidentRef: "INC-1", AlertTarget: "pager"}
	if _, err := service.EnableBreakGlass(t.Context(), command, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.break_glass_active_conflict" || !errors.Is(err, errBreakGlassProbe) {
		t.Fatalf("create err=%v", err)
	}
	for _, invalid := range []OperationsBreakGlassEnableCommand{
		{DurationSeconds: 0, IncidentRef: "INC", AlertTarget: "pager"},
		{DurationSeconds: int(operationspolicy.MaximumBreakGlassDuration/time.Second) + 1, IncidentRef: "INC", AlertTarget: "pager"},
		{DurationSeconds: 1, IncidentRef: " ", AlertTarget: "pager"},
		{DurationSeconds: 1, IncidentRef: "INC", AlertTarget: " "},
	} {
		if _, err := service.EnableBreakGlass(t.Context(), invalid, "invalid", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.break_glass_command_invalid" {
			t.Fatalf("invalid=%#v err=%v", invalid, err)
		}
	}
}

func TestOperationsBreakGlassDisableReadAlreadyRevokedAndAlertEdges(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	principal := operationsAdminPrincipal()
	command := OperationsBreakGlassDisableCommand{ExpectedRevision: 1, Reason: "done", IncidentRef: "INC-1"}
	for _, test := range []struct {
		name       string
		grant      *operationsmodel.OperationsBreakGlassGrant
		getErr     error
		revokeErr  error
		alertFail  bool
		code       string
		expectDone bool
	}{
		{"missing", nil, nil, nil, false, "backend.operations.break_glass_not_found", false},
		{"read", nil, errBreakGlassProbe, nil, false, "backend.operations.break_glass_not_found", false},
		{"workspace", &operationsmodel.OperationsBreakGlassGrant{ID: "grant", WorkspaceID: "other", State: operationsmodel.OperationsBreakGlassActive, Revision: 1}, nil, nil, false, "backend.operations.break_glass_not_found", false},
		{"revoked", &operationsmodel.OperationsBreakGlassGrant{ID: "grant", WorkspaceID: "workspace-a", State: operationsmodel.OperationsBreakGlassRevoked, Revision: 2}, nil, nil, false, "", true},
		{"revoke", &operationsmodel.OperationsBreakGlassGrant{ID: "grant", WorkspaceID: "workspace-a", State: operationsmodel.OperationsBreakGlassActive, Revision: 1}, nil, errBreakGlassProbe, false, "backend.operations.break_glass_revision_conflict", false},
		{"alert", &operationsmodel.OperationsBreakGlassGrant{ID: "grant", WorkspaceID: "workspace-a", State: operationsmodel.OperationsBreakGlassActive, Revision: 1}, nil, nil, true, "backend.operations.break_glass_alert_failed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			grants := map[string]operationsmodel.OperationsBreakGlassGrant{}
			if test.grant != nil {
				grants[test.grant.ID] = *test.grant
			}
			repository := &breakGlassRepositoryProbe{grants: grants, getErr: test.getErr, revokeErr: test.revokeErr}
			service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { return test.name })
			_ = service.RegisterBreakGlass(repository, &breakGlassAlertProbe{fail: test.alertFail})
			result, err := service.DisableBreakGlass(t.Context(), "grant", command, "key", principal)
			if test.expectDone {
				if err != nil || result.Grant.State != operationsmodel.OperationsBreakGlassRevoked {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else if apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := serviceWithBreakGlass(t, &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}}).DisableBreakGlass(t.Context(), "", OperationsBreakGlassDisableCommand{}, "key", principal); apperror.CodeOf(err) != "backend.operations.break_glass_command_invalid" {
		t.Fatalf("invalid disable err=%v", err)
	}
}

func serviceWithBreakGlass(t *testing.T, repository *breakGlassRepositoryProbe) *OperationsApplicationService {
	t.Helper()
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "break-list" })
	if err := service.RegisterBreakGlass(repository, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestOperationsBreakGlassReplayStartFinishAndListFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	principal := operationsAdminPrincipal()
	command := OperationsBreakGlassEnableCommand{DurationSeconds: 60, ApproverIDs: []string{"a", "b"}, Reason: "incident", IncidentRef: "INC-1", AlertTarget: "pager"}
	repository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string { return "break-replay" })
	_ = service.RegisterBreakGlass(repository, &breakGlassAlertProbe{})
	if _, err := service.EnableBreakGlass(t.Context(), command, "key", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.EnableBreakGlass(t.Context(), command, "key", principal); apperror.CodeOf(err) != "backend.operations.break_glass_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}

	for _, test := range []struct {
		name   string
		failAt int
		code   string
	}{
		{"authorization", 0, "backend.workspace_scope_required"},
		{"start", 1, "backend.operations.transition_failed"},
		{"finish", 2, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: base, failAt: test.failAt}
			service := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string { return test.name })
			_ = service.RegisterBreakGlass(&breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}}, &breakGlassAlertProbe{})
			testPrincipal := principal
			if test.name == "authorization" {
				testPrincipal.WorkspaceID = ""
			}
			if _, err := service.EnableBreakGlass(t.Context(), command, "key", testPrincipal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
	listService := serviceWithBreakGlass(t, &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}, listErr: errBreakGlassProbe})
	if _, err := listService.ListBreakGlass(t.Context(), 10, principal); !errors.Is(err, errBreakGlassProbe) {
		t.Fatalf("list err=%v", err)
	}
}
