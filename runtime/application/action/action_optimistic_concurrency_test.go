package action

import (
	"testing"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func optimisticActionFixture(field string) (definitionmodel.ActionSchema, actionmodel.ActionInvocation, []transactionmodel.RecordMutationCommit) {
	action := definitionmodel.ActionSchema{Key: "case.assign", ObjectKey: "case", OptimisticConcurrency: true, ConcurrencyField: field}
	invocation := actionmodel.ActionInvocation{RecordID: "case-1", Input: map[string]any{"expected_updated_at": "client-v1", "expected_version": 7}}
	commits := []transactionmodel.RecordMutationCommit{{
		Operation: "update", Object: definitionmodel.ObjectSchema{Key: "case"},
		Record:     recordmodel.Record{ID: "case-1"},
		Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: "planner-current"},
	}}
	return action, invocation, commits
}

func TestActionOptimisticConcurrencyInjectsGovernedUpdatedAt(t *testing.T) {
	action, invocation, commits := optimisticActionFixture("updated_at")
	governed, err := enforceActionOptimisticConcurrency(action, invocation, commits)
	if err != nil {
		t.Fatal(err)
	}
	if got := governed[0].Optimistic.ExpectedUpdatedAt; got != "client-v1" {
		t.Fatalf("expected Runtime-owned caller token, got %q", got)
	}
	if commits[0].Optimistic.ExpectedUpdatedAt != "planner-current" {
		t.Fatal("input commits were mutated")
	}
}

func TestActionOptimisticConcurrencyInjectsBusinessVersionPredicate(t *testing.T) {
	action, invocation, commits := optimisticActionFixture("version")
	governed, err := enforceActionOptimisticConcurrency(action, invocation, commits)
	if err != nil {
		t.Fatal(err)
	}
	if governed[0].Optimistic.ExpectedVersion == nil || *governed[0].Optimistic.ExpectedVersion != 7 {
		t.Fatalf("optimistic metadata=%+v", governed[0].Optimistic)
	}
	if len(governed[0].Predicates) != 1 || governed[0].Predicates[0].Field != "version" || governed[0].Predicates[0].ErrorCode != "backend.record.version_conflict" {
		t.Fatalf("predicates=%+v", governed[0].Predicates)
	}
}

func TestActionOptimisticConcurrencyFailsClosed(t *testing.T) {
	action, invocation, commits := optimisticActionFixture("updated_at")
	delete(invocation.Input, "expected_updated_at")
	if _, err := enforceActionOptimisticConcurrency(action, invocation, commits); apperror.CodeOf(err) != "backend.action.expected_updated_at_required" {
		t.Fatalf("missing-token error=%v", err)
	}
	invocation.Input["expected_updated_at"] = "client-v1"
	commits[0].Record.ID = "different"
	if _, err := enforceActionOptimisticConcurrency(action, invocation, commits); apperror.CodeOf(err) != "backend.action.optimistic_target_mutation_required" {
		t.Fatalf("missing-target error=%v", err)
	}
	commits = append(commits, commits[0])
	commits[0].Record.ID, commits[1].Record.ID = "case-1", "case-1"
	if _, err := enforceActionOptimisticConcurrency(action, invocation, commits); apperror.CodeOf(err) != "backend.action.optimistic_target_mutation_ambiguous" {
		t.Fatalf("ambiguous-target error=%v", err)
	}
}

func TestActionOptimisticConflictUsesRecordContractCode(t *testing.T) {
	err := mutation.MutationConflict("case", "case-1", mutation.MutationConflictOptimistic, nil)
	if code := apperror.CodeOf(normalizeActionInvocationError(err)); code != "backend.record.version_conflict" {
		t.Fatalf("code=%q", code)
	}
}
