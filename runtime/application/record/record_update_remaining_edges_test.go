package record

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestRecordUpdatePersistedScopeFailureAtReadAndWriteStages(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	failure := errors.New("scope failed")
	for stage := 1; stage <= 2; stage++ {
		t.Run(map[int]string{1: "read", 2: "write"}[stage], func(t *testing.T) {
			repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Before", "status": "open", "age": float64(20), "version": float64(1)}}}
			dependencies := recordUpdateEdgeDependencies(repository)
			calls := 0
			dependencies.CanAccessScope = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error) {
				calls++
				if calls == stage {
					return false, failure
				}
				return true, nil
			}
			if _, err := NewRecordUpdateApplicationService(dependencies).Update(t.Context(), "customer", "customer-1", map[string]any{"name": "After"}, principal); !errors.Is(err, failure) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestPlanRecordUpdateReturnsPersistedScopeFailure(t *testing.T) {
	failure := errors.New("scope failed")
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Before"}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.CanAccessScope = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error) {
		return false, failure
	}
	if _, _, err := NewRecordUpdateApplicationService(dependencies).PlanUpdateMutation(t.Context(), "customer", "customer-1", map[string]any{"name": "After"}, recordUpdateFinalPrincipal()); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordUpdateTriggerDeduplication(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	newService := func() (*RecordUpdateApplicationService, *updateEdgeRepository, *int) {
		repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Before", "status": "open", "age": float64(20), "version": float64(1)}}}
		dependencies := recordUpdateEdgeDependencies(repository)
		workflowCalls := 0
		dependencies.UpdatedTriggers = func(string, map[string]any, map[string]any) []string { return []string{"", "sync", " sync "} }
		dependencies.PrepareWorkflow = func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			workflowCalls++
			return []workflowmodel.WorkflowExecution{{ID: "workflow"}}, nil
		}
		return NewRecordUpdateApplicationService(dependencies), repository, &workflowCalls
	}
	service, repository, workflowCalls := newService()
	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceWorkflow, WorkflowKey: "customer.sync", WorkflowTriggers: []string{"sync", "", "extra"}})
	if _, err := service.Update(ctx, "customer", "customer-1", map[string]any{"name": "After"}, principal); err != nil {
		t.Fatal(err)
	}
	if *workflowCalls != 2 || len(repository.commit.WorkflowIntents) != 2 {
		t.Fatalf("workflow=%d commit=%+v", *workflowCalls, repository.commit)
	}
}

func TestRecordUpdateCommitErrorPreservesBusinessConflict(t *testing.T) {
	err := mutation.PolicyConflict("backend.business.conflict", "customer", "customer-1", "status")
	mapped := recordUpdateCommitError(err)
	if apperror.KindOf(mapped) != apperror.KindConflict || apperror.CodeOf(mapped) != "backend.business.conflict" {
		t.Fatalf("mapped=%v", mapped)
	}
}

func TestChangedRelationDataPresenceTransitions(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "relation"}}}
	if changed := changedRelationData(object, map[string]any{"owner": "user-1"}, map[string]any{}); len(changed) != 0 {
		t.Fatalf("removed relation=%v", changed)
	}
	if changed := changedRelationData(object, map[string]any{}, map[string]any{"owner": "user-1"}); changed["owner"] != "user-1" {
		t.Fatalf("added relation=%v", changed)
	}
}
