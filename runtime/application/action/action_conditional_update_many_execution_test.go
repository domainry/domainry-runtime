package action

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestConditionalUpdateManyIsOneSetCallWithoutPerRecordLookupOrMutation(t *testing.T) {
	listCalls, planCalls := 0, 0
	object := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "staff_id", Type: "text"}, {Key: "status", Type: "text"}, {Key: "clock_out", Type: "datetime"}}}
	records := []recordmodel.Record{
		{ID: "shift-1", OwnerOrgID: "store-a", UpdatedAt: "v1", Data: map[string]any{"staff_id": "staff-1", "status": "working"}},
		{ID: "shift-2", OwnerOrgID: "store-a", UpdatedAt: "v1", Data: map[string]any{"staff_id": "staff-2", "status": "working"}},
	}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			ListRecords: func(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				listCalls++
				if objectKey != "shift" || query.LockIntent != recordmodel.RecordQueryLockForUpdate || query.PageSize != 2 || !query.SkipTotal || query.FilterExpression == nil {
					t.Fatalf("set query=%+v", query)
				}
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
					t.Fatal("set query is outside Action transaction")
				}
				return recordmodel.RecordPageResult{Items: records, PageSize: 2}, nil
			},
			PlanConditionalUpdateLockedRecord: func(_ context.Context, objectKey string, before recordmodel.Record, input transactionmodel.ConditionalUpdateInput, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				planCalls++
				updated := before
				updated.UpdatedAt = "v2"
				updated.UpdateBy = "manager"
				updated.Data = map[string]any{"staff_id": before.Data["staff_id"], "status": input.Patch["status"], "clock_out": input.Patch["clock_out"]}
				mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: "shift.bulk_clock_out", CorrelationID: "correlation", ApplicationSchemaRevision: "schema"})
				if err != nil {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
				}
				plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: updated}, before.Data)
				return plan, updated, err
			},
		},
		invocation: actionmodel.ActionInvocation{IdempotencyKey: "bulk-1"},
		action: definitionmodel.ActionSchema{Key: "shift.bulk_clock_out", ObjectKey: "shift", EffectSet: &definitionmodel.ActionEffectSet{
			Read:  []definitionmodel.ActionObjectEffect{{ObjectKey: "shift", Operations: []string{"conditional_update_many"}}},
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "shift", Operations: []string{"conditional_update_many"}}},
		}},
		unitOfWork:   newActionTestUnitOfWork(),
		objectGrants: []runtimeext.ActionObjectCapability{{ObjectKey: "shift", Operations: []string{"conditional_update_many"}}},
	}
	request := runtimeext.ConditionalUpdateManyRequest{
		ObjectKey: "shift", ExpectedCount: 2,
		Filters: []runtimeext.Filter{{Field: "staff_id", Operator: "in", Values: []any{"staff-1", "staff-2"}}, {Field: "status", Operator: "eq", Value: "working"}},
		Fields:  map[string]any{"status": "finished", "clock_out": "2026-09-06T23:00:00Z"},
	}
	result, err := execution.ConditionalUpdateMany(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if listCalls != 1 || planCalls != 2 || result.AffectedCount != 2 || !reflect.DeepEqual(result.RecordIDs, []string{"shift-1", "shift-2"}) {
		t.Fatalf("list=%d plans=%d result=%+v", listCalls, planCalls, result)
	}
	if len(execution.plans) != 0 || len(execution.setCommits) != 1 {
		t.Fatalf("per-record plans=%d set commits=%d", len(execution.plans), len(execution.setCommits))
	}
	commit := execution.setCommits[0]
	if commit.Operation != "conditional_update_many" || commit.SetExpectedAffected != 2 || !reflect.DeepEqual(commit.SetRecordIDs, []string{"shift-1", "shift-2"}) || !reflect.DeepEqual(commit.Record.Data, request.Fields) {
		t.Fatalf("set commit=%+v", commit)
	}
	execution.unitOfWork.rollBack(t.Context())
}
