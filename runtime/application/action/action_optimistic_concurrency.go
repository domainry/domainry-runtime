package action

import (
	"fmt"
	"strconv"
	"strings"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// enforceActionOptimisticConcurrency turns the published Action contract into
// a Runtime-owned compare-and-set precondition. Generated business code may
// use the client token while deriving its result, but correctness never
// depends on the Handler copying that token into its mutation.
func enforceActionOptimisticConcurrency(action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation, commits []transactionmodel.RecordMutationCommit) ([]transactionmodel.RecordMutationCommit, error) {
	if !action.OptimisticConcurrency {
		return commits, nil
	}
	if strings.TrimSpace(invocation.RecordID) == "" {
		return nil, optimisticContractError("backend.action.optimistic_record_required", action, "")
	}
	index, err := optimisticTargetCommit(action, invocation.RecordID, commits)
	if err != nil {
		return nil, err
	}
	result := append([]transactionmodel.RecordMutationCommit(nil), commits...)
	commit := result[index]
	field := strings.TrimSpace(action.ConcurrencyField)
	if field == "" {
		field = "updated_at"
	}
	if field == "updated_at" {
		expected := strings.TrimSpace(fmt.Sprint(invocation.Input["expected_updated_at"]))
		if expected == "" || expected == "<nil>" {
			return nil, optimisticContractError("backend.action.expected_updated_at_required", action, field)
		}
		// The mutation planner normally carries the version it observed while
		// planning. Replace it with the caller's governed token so a Handler
		// cannot accidentally turn a stale command into a fresh write.
		commit.ExpectedUpdatedAt = ""
		commit.Optimistic.ExpectedUpdatedAt = expected
		result[index] = commit
		return result, nil
	}

	expected, ok := optimisticExpectedVersion(invocation.Input["expected_version"])
	if !ok {
		return nil, optimisticContractError("backend.action.expected_version_required", action, field)
	}
	commit.Optimistic.ExpectedVersion = &expected
	found := false
	for predicateIndex := range commit.Predicates {
		predicate := &commit.Predicates[predicateIndex]
		if strings.TrimSpace(predicate.Field) != field {
			continue
		}
		if strings.TrimSpace(predicate.Operator) != "eq" || fmt.Sprint(predicate.Value) != strconv.FormatInt(expected, 10) {
			return nil, optimisticContractError("backend.action.optimistic_precondition_mismatch", action, field)
		}
		predicate.ErrorCode = "backend.record.version_conflict"
		found = true
	}
	if !found {
		commit.Predicates = append(commit.Predicates, transactionmodel.MutationPredicate{
			Field: field, Operator: "eq", Value: expected, ErrorCode: "backend.record.version_conflict",
		})
	}
	result[index] = commit
	return result, nil
}

func optimisticTargetCommit(action definitionmodel.ActionSchema, recordID string, commits []transactionmodel.RecordMutationCommit) (int, error) {
	target := -1
	for index, commit := range commits {
		commitRecordID := strings.TrimSpace(commit.RecordID)
		if commitRecordID == "" {
			commitRecordID = strings.TrimSpace(commit.Record.ID)
		}
		if strings.TrimSpace(commit.Object.Key) != strings.TrimSpace(action.ObjectKey) || commitRecordID != strings.TrimSpace(recordID) || strings.TrimSpace(commit.Operation) == "create" {
			continue
		}
		if target >= 0 {
			return -1, optimisticContractError("backend.action.optimistic_target_mutation_ambiguous", action, strings.TrimSpace(action.ConcurrencyField))
		}
		target = index
	}
	if target < 0 {
		return -1, optimisticContractError("backend.action.optimistic_target_mutation_required", action, strings.TrimSpace(action.ConcurrencyField))
	}
	return target, nil
}

func optimisticExpectedVersion(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func optimisticContractError(code string, action definitionmodel.ActionSchema, field string) error {
	return apperror.New(apperror.KindBadRequest, code, nil, map[string]string{
		"action": strings.TrimSpace(action.Key), "field": strings.TrimSpace(field),
	})
}
