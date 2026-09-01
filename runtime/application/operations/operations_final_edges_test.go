package operations

import (
	"fmt"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestOperationsCoreFinalConditionEdges(t *testing.T) {
	admin := operationsTestAdmin()
	workspaceRequest := OperationsSubmitRequest{Kind: "retention.cleanup", Permission: "undeclared", ResourceType: "retention_policy", Reason: "test"}
	systemRequest := OperationsSubmitRequest{Kind: "runtime.maintenance.enable", Permission: "undeclared", ResourceType: "runtime", Reason: "test"}
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "edge" })
	if _, _, err := service.Submit(t.Context(), workspaceRequest, "key", admin); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("workspace permission declaration error = %v", err)
	}
	if _, _, err := service.SubmitSystem(t.Context(), systemRequest, "key", "runtime", admin); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("system permission declaration error = %v", err)
	}

	validRequest := OperationsSubmitRequest{Kind: "retention.cleanup", Permission: "runtime.retention.execute", ResourceType: "retention_policy", Reason: "test"}
	if _, _, err := NewOperationsApplicationService(nil, nil, nil, nil).Submit(t.Context(), validRequest, "key", admin); apperror.CodeOf(err) != "backend.operations.repository_unavailable" {
		t.Fatalf("nil repository error = %v", err)
	}
	for name, principal := range map[string]principalmodel.Principal{
		"unknown":    principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: admin.WorkspaceID}},
		"blank user": principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: "  "}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operationsAuthorize(principal, "workspace.admin"); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("authorization error = %v", err)
			}
		})
	}
	t.Run("workspace administrator does not expand to runtime operations", func(t *testing.T) {
		principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
			WorkspaceID: admin.WorkspaceID,
			UserID:      "tenant-admin"},
		}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}},
		)
		if err := operationsAuthorize(principal, "runtime.worker.control"); apperror.KindOf(err) != apperror.KindForbidden {
			t.Fatalf("workspace.admin expanded to runtime.worker.control: %v", err)
		}
		accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
			role.Permissions = append(role.Permissions, "runtime.worker.control")
		})
		if err := operationsAuthorize(principal, "runtime.worker.control"); err != nil {
			t.Fatalf("exact runtime ops permission rejected: %v", err)
		}
	})

	// Exercise the bounded-list valid range, complementing the zero and over-200 cases.
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service = NewOperationsApplicationService(repository, nil, nil, func() string { return "receipt" })
	if _, err := service.Receipts(t.Context(), "", 10, admin); err != nil || repository.lastLimit != 10 {
		t.Fatalf("valid list limit=%d err=%v", repository.lastLimit, err)
	}

	receipt, _, err := service.Submit(t.Context(), validRequest, "finish", admin)
	if err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")
	started, err := service.Start(t.Context(), receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		t.Fatal(err)
	}
	started.Command.Status = operationsmodel.OperationsStatusSucceeded
	started.NextAction = "verified"
	finished, err := service.Finish(t.Context(), started, scope)
	if err != nil || len(finished.RelatedIDs) != 0 {
		t.Fatalf("finish with empty resource id=%#v err=%v", finished, err)
	}

	other, _, err := service.Submit(t.Context(), validRequest, "direct-transition", admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.transition(t.Context(), other.Command.ID, operationsmodel.OperationsStatusCreated, operationsmodel.OperationsStatusFailed, other.Command.Scope); err != nil {
		t.Fatalf("non-start transition error = %v", err)
	}
}

func TestOperationsBreakGlassFinalAvailabilityReplayAndListEdges(t *testing.T) {
	admin := operationsTestAdmin()
	var nilService *OperationsApplicationService
	if err := NewOperationsApplicationService(nil, nil, nil, nil).RegisterBreakGlass(&breakGlassRepositoryProbe{}, nil); apperror.CodeOf(err) != "backend.operations.break_glass_registration_invalid" {
		t.Fatalf("nil alert registration error = %v", err)
	}
	if _, err := nilService.EnableBreakGlass(t.Context(), OperationsBreakGlassEnableCommand{}, "key", admin); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("nil enable service error = %v", err)
	}
	partial := NewOperationsApplicationService(nil, nil, nil, nil)
	partial.breakGlass = &breakGlassRepositoryProbe{}
	if _, err := partial.EnableBreakGlass(t.Context(), OperationsBreakGlassEnableCommand{}, "key", admin); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("missing enable alerts error = %v", err)
	}
	if _, err := nilService.DisableBreakGlass(t.Context(), "grant", OperationsBreakGlassDisableCommand{}, "key", admin); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("nil disable service error = %v", err)
	}
	partial = NewOperationsApplicationService(nil, nil, nil, nil)
	partial.breakGlass = &breakGlassRepositoryProbe{}
	if _, err := partial.DisableBreakGlass(t.Context(), "grant", OperationsBreakGlassDisableCommand{}, "key", admin); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("missing disable alerts error = %v", err)
	}
	partial = NewOperationsApplicationService(nil, nil, nil, nil)
	partial.breakGlassAlerts = &breakGlassAlertProbe{}
	if _, err := partial.DisableBreakGlass(t.Context(), "grant", OperationsBreakGlassDisableCommand{}, "key", admin); apperror.CodeOf(err) != "backend.operations.break_glass_unavailable" {
		t.Fatalf("missing disable repository error = %v", err)
	}

	createChanged := false
	enableRepository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}, createChanged: &createChanged}
	enableLedger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	enableSequence := 0
	enableService := NewOperationsApplicationService(enableLedger, nil, nil, func() string {
		enableSequence++
		return fmt.Sprintf("enable-%d", enableSequence)
	})
	if err := enableService.RegisterBreakGlass(enableRepository, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	enableCommand := OperationsBreakGlassEnableCommand{DurationSeconds: 60, ApproverIDs: []string{"a", "b"}, Reason: "incident", IncidentRef: "INC-1", AlertTarget: "pager"}
	if _, err := enableService.EnableBreakGlass(t.Context(), enableCommand, "enable", admin); apperror.CodeOf(err) != "backend.operations.break_glass_active_conflict" {
		t.Fatalf("unchanged create error = %v", err)
	}

	enableRepository.createChanged = nil
	if _, err := enableService.EnableBreakGlass(t.Context(), enableCommand, "enable-success", admin); err != nil {
		t.Fatal(err)
	}
	for key, receipt := range enableLedger.receipts {
		if receipt.Command.Kind == "break_glass.enable" && receipt.Command.IdempotencyKey == "enable-success" {
			receipt.Command.Status = operationsmodel.OperationsStatusCreated
			enableLedger.receipts[key] = receipt
		}
	}
	if _, err := enableService.EnableBreakGlass(t.Context(), enableCommand, "enable-success", admin); apperror.CodeOf(err) != "backend.operations.break_glass_active_conflict" {
		t.Fatalf("non-succeeded enable replay error = %v", err)
	}

	validDisable := OperationsBreakGlassDisableCommand{ExpectedRevision: 1, Reason: "done", IncidentRef: "INC-1"}
	service := serviceWithBreakGlass(t, &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}})
	for name, command := range map[string]OperationsBreakGlassDisableCommand{
		"revision": {Reason: "done", IncidentRef: "INC-1"},
		"reason":   {ExpectedRevision: 1, IncidentRef: "INC-1"},
		"incident": {ExpectedRevision: 1, Reason: "done"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.DisableBreakGlass(t.Context(), "grant", command, "invalid", admin); apperror.CodeOf(err) != "backend.operations.break_glass_command_invalid" {
				t.Fatalf("invalid command error = %v", err)
			}
		})
	}
	denied := admin
	denied.WorkspaceID = ""
	if _, err := service.DisableBreakGlass(t.Context(), "grant", validDisable, "denied", denied); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("disable submission error = %v", err)
	}

	now := time.Date(2026, 7, 20, 4, 5, 6, 0, time.UTC)
	grant := operationsmodel.OperationsBreakGlassGrant{ID: "grant", WorkspaceID: admin.WorkspaceID, State: operationsmodel.OperationsBreakGlassActive, Revision: 1, IncidentRef: "INC-1", AlertTarget: "pager", AuditEventID: "audit"}
	repository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{grant.ID: grant}}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service = NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string { return "disable-replay" })
	if err := service.RegisterBreakGlass(repository, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	first, err := service.DisableBreakGlass(t.Context(), grant.ID, validDisable, "disable", admin)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.DisableBreakGlass(t.Context(), grant.ID, validDisable, "disable", admin)
	if err != nil || replayed.Grant.State != operationsmodel.OperationsBreakGlassRevoked {
		t.Fatalf("disable replay=%#v err=%v", replayed, err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.DisableBreakGlass(t.Context(), grant.ID, validDisable, "disable", admin); apperror.CodeOf(err) != "backend.operations.break_glass_receipt_invalid" {
		t.Fatalf("malformed disable replay error = %v", err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusCreated
	})
	if result, err := service.DisableBreakGlass(t.Context(), grant.ID, validDisable, "disable", admin); err != nil || result.Grant.State != operationsmodel.OperationsBreakGlassRevoked {
		t.Fatalf("non-succeeded disable replay error = %v", err)
	}
	startBase := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	startLedger := &operationsUpdateFailureProbe{operationsRepositoryProbe: startBase, failAt: 1}
	startService := NewOperationsApplicationService(startLedger, nil, func() time.Time { return now }, func() string { return "disable-start" })
	if err := startService.RegisterBreakGlass(&breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{grant.ID: grant}}, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	if _, err := startService.DisableBreakGlass(t.Context(), grant.ID, validDisable, "disable-start", admin); apperror.CodeOf(err) != "backend.operations.transition_failed" {
		t.Fatalf("disable start error = %v", err)
	}

	future := now.Add(time.Minute)
	listRepository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{
		"revoked": {ID: "revoked", WorkspaceID: admin.WorkspaceID, State: operationsmodel.OperationsBreakGlassRevoked},
		"future":  {ID: "future", WorkspaceID: admin.WorkspaceID, State: operationsmodel.OperationsBreakGlassActive, ExpiresAt: future},
	}}
	listService := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { return "break-list" })
	if err := listService.RegisterBreakGlass(listRepository, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	listed, err := listService.ListBreakGlass(t.Context(), 10, admin)
	if err != nil || len(listed) != 2 {
		t.Fatalf("break-glass list=%#v err=%v", listed, err)
	}
	for _, item := range listed {
		if item.State == operationsmodel.OperationsBreakGlassExpired {
			t.Fatalf("unexpired/non-active grant was expired: %#v", item)
		}
	}
	_ = first
}
