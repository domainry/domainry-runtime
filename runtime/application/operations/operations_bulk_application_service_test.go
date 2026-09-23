package operations

import (
	"context"
	"fmt"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type bulkDeadLetterOwnerProbe struct {
	items        map[string]OperationsDeadLetterItem
	inspectErr   map[string]error
	actErr       map[string]error
	inspects     int
	acts         int
	operationIDs []string
}

func (p *bulkDeadLetterOwnerProbe) Inspect(_ context.Context, id string, _ principalmodel.Principal) (OperationsDeadLetterItem, error) {
	p.inspects++
	if err := p.inspectErr[id]; err != nil {
		return OperationsDeadLetterItem{}, err
	}
	item, found := p.items[id]
	if !found {
		return OperationsDeadLetterItem{}, apperror.New(apperror.KindNotFound, "not_found", nil, nil)
	}
	return item, nil
}
func (p *bulkDeadLetterOwnerProbe) Act(ctx context.Context, id, action, _, _ string, _ principalmodel.Principal) (OperationsDeadLetterItem, error) {
	p.acts++
	p.operationIDs = append(p.operationIDs, requestcontext.OwnerExecutionID(ctx))
	if err := p.actErr[id]; err != nil {
		return OperationsDeadLetterItem{}, err
	}
	item := p.items[id]
	item.Status = action + "d"
	p.items[id] = item
	return item, nil
}

func TestOperationsBulkRequiresMatchingDryRunAndReplaysPerItemResults(t *testing.T) {
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	sequence := 0
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { sequence++; return fmt.Sprintf("bulk-%d", sequence) })
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{
		"a": {Owner: "probe", ID: "a", Status: "dead_letter", AllowedActions: []string{"retry"}},
		"b": {Owner: "probe", ID: "b", Status: "resolved", AllowedActions: []string{"retry"}},
	}}
	if err := service.RegisterDeadLetterOwner("probe", owner); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.dry_run_bulk_dead_letters", "runtime.operations.apply_bulk_dead_letters"}})
	plan, err := service.DryRunBulkDeadLetters(t.Context(), OperationsBulkDryRunRequest{Owner: "probe", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"b", "a", "a"}, Status: "dead_letter"}, Limit: 2, Reason: "incident recovery"}, "dry-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 2 || !plan.Candidates[0].Eligible || plan.Candidates[1].Eligible || plan.ConfirmationToken == "" || plan.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded {
		t.Fatalf("plan=%#v", plan)
	}
	if _, err := service.ApplyBulkDeadLetters(t.Context(), OperationsBulkApplyRequest{DryRunOperationID: plan.DryRunOperationID, ConfirmationToken: "wrong", Confirm: true, Reason: "incident recovery"}, "apply-key", principal); apperror.CodeOf(err) != "backend.operations.bulk_confirmation_mismatch" {
		t.Fatalf("mismatch err=%v", err)
	}
	request := OperationsBulkApplyRequest{DryRunOperationID: plan.DryRunOperationID, ConfirmationToken: plan.ConfirmationToken, Confirm: true, Reason: "incident recovery"}
	result, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply-key", principal)
	if err != nil || result.Succeeded != 1 || result.Failed != 0 || len(result.Items) != 1 || owner.acts != 1 {
		t.Fatalf("result=%#v acts=%d err=%v", result, owner.acts, err)
	}
	if len(owner.operationIDs) != 1 || owner.operationIDs[0] != result.Receipt.Command.ID {
		t.Fatalf("owner operation ids=%v receipt=%q", owner.operationIDs, result.Receipt.Command.ID)
	}
	current := owner.items["a"]
	current.Status = "archived"
	owner.items["a"] = current
	replayed, err := service.ApplyBulkDeadLetters(t.Context(), request, "apply-key", principal)
	if err != nil || replayed.Succeeded != 1 || replayed.Items[0].Item.Status != "archived" || owner.acts != 1 || owner.inspects != 3 || replayed.Receipt.Command.ID != result.Receipt.Command.ID {
		t.Fatalf("replay=%#v inspects=%d acts=%d err=%v", replayed, owner.inspects, owner.acts, err)
	}
}

func TestOperationsBulkEnforcesExplicitBoundedFilter(t *testing.T) {
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil)
	_ = service.RegisterDeadLetterOwner("probe", &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.dry_run_bulk_dead_letters"}})
	for _, request := range []OperationsBulkDryRunRequest{
		{Owner: "probe", Action: "retry", Limit: 1, Reason: "test"},
		{Owner: "probe", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a", "b"}}, Limit: 1, Reason: "test"},
		{Owner: "probe", Action: "retry", Filter: OperationsBulkFilter{IDs: []string{"a"}}, Limit: operationsBulkMaximumItems + 1, Reason: "test"},
	} {
		if _, err := service.DryRunBulkDeadLetters(t.Context(), request, "key", principal); apperror.CodeOf(err) != "backend.operations.bulk_filter_invalid" {
			t.Fatalf("request=%#v err=%v", request, err)
		}
	}
}

func TestOperationsDeadLetterActionReplaysReceiptWithoutDuplicateOwnerMutation(t *testing.T) {
	sequence := 0
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { sequence++; return fmt.Sprintf("dead-letter-%d", sequence) })
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"item-1": {Owner: "probe", ID: "item-1", Status: "dead_letter", CorrelationID: "correlation-1", BusinessKey: "order-42", EvidenceRef: "evidence-7", AllowedActions: []string{"resolve"}}}}
	if err := service.RegisterDeadLetterOwner("probe", owner); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.resolve_dead_letter"}})
	request := OperationsDeadLetterActionRequest{Reason: "verified and resolved", Reference: "INC-42"}
	first, err := service.ActOnDeadLetter(t.Context(), "probe", "item-1", "resolve", request, "resolve-key", principal)
	if err != nil || owner.acts != 1 || first.Item.CorrelationID != "correlation-1" || first.Item.BusinessKey != "order-42" || first.Item.EvidenceRef != "evidence-7" {
		t.Fatalf("first=%#v acts=%d err=%v", first, owner.acts, err)
	}
	if len(owner.operationIDs) != 1 || owner.operationIDs[0] != first.Receipt.Command.ID {
		t.Fatalf("owner operation ids=%v receipt=%q", owner.operationIDs, first.Receipt.Command.ID)
	}
	current := owner.items["item-1"]
	current.Status = "archived"
	owner.items["item-1"] = current
	replayed, err := service.ActOnDeadLetter(t.Context(), "probe", "item-1", "resolve", request, "resolve-key", principal)
	if err != nil || owner.acts != 1 || owner.inspects != 1 || replayed.Item.Status != "archived" || replayed.Receipt.Command.ID != first.Receipt.Command.ID {
		t.Fatalf("replay=%#v inspects=%d acts=%d err=%v", replayed, owner.inspects, owner.acts, err)
	}
}
