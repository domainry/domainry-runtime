package action

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestConditionalUpdateClassifiesOnlyDeclaredChangesAfterActionStartAsRecordConflict(t *testing.T) {
	insufficient := apperror.New(apperror.KindConflict, "inventory.insufficient_available", nil, nil)
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			PlanConditionalUpdate: func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, insufficient
			},
		},
		unitOfWork: &actionUnitOfWork{claim: actionmodel.ActionExecutionClaimResult{
			Execution: actionmodel.ActionBusinessExecution{CreatedAt: "2026-07-26T12:00:00Z"},
		}},
		observedRecords: map[string]recordmodel.Record{
			"inventory_balance\x00balance-1": {ID: "balance-1", UpdatedAt: "2026-07-26T12:00:01Z"},
		},
	}
	declared := runtimeext.RecordMutation{
		Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "inventory_balance", RecordID: "balance-1",
		Predicates: []runtimeext.Predicate{
			{Field: "available_qty", Operator: "eq", Value: int64(5), ErrorCode: "backend.record.conflict"},
			{Field: "available_qty", Operator: "gte", Value: int64(4), ErrorCode: "inventory.insufficient_available"},
		},
	}
	if _, _, err := execution.planConditionalUpdate(t.Context(), declared); apperror.CodeOf(err) != "backend.record.conflict" || !errors.Is(err, insufficient) {
		t.Fatalf("declared concurrent error=%v code=%s", err, apperror.CodeOf(err))
	}

	ordinary := declared
	ordinary.Predicates = ordinary.Predicates[1:]
	if _, _, err := execution.planConditionalUpdate(t.Context(), ordinary); !errors.Is(err, insufficient) || apperror.CodeOf(err) != "inventory.insufficient_available" {
		t.Fatalf("ordinary predicate error=%v code=%s", err, apperror.CodeOf(err))
	}

	execution.observedRecords["inventory_balance\x00balance-1"] = recordmodel.Record{
		ID: "balance-1", UpdatedAt: "2026-07-26T11:59:59Z",
	}
	if _, _, err := execution.planConditionalUpdate(t.Context(), declared); !errors.Is(err, insufficient) || apperror.CodeOf(err) != "inventory.insufficient_available" {
		t.Fatalf("preexisting predicate error=%v code=%s", err, apperror.CodeOf(err))
	}
}
