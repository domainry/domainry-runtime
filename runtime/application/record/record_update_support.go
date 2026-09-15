package record

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
)

const recordMutationFailureCompletionTimeout = 5 * time.Second

func finalizeRecordMutationFailure(ctx context.Context, execution *recordruntime.RecordMutationExecutionRuntime, claim recordmodel.RecordMutationClaimResult, failure error) error {
	if failure == nil || execution == nil || strings.TrimSpace(claim.Execution.ID) == "" {
		return failure
	}
	completionContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordMutationFailureCompletionTimeout)
	defer cancel()
	if err := execution.Fail(completionContext, claim, failure); err != nil {
		return err
	}
	return failure
}

func (s *RecordUpdateApplicationService) now() time.Time {
	if s.dependencies.Now != nil {
		return s.dependencies.Now()
	}
	return time.Now()
}

// changedRelationData limits update-time relation checks to references that the
// mutation actually changed. Existing references were validated when they were
// written; resolving all of them again is both unnecessary and can deadlock a
// transaction-bound projection reader on single-connection databases.
func changedRelationData(object definitionmodel.ObjectSchema, beforeData, nextData map[string]any) map[string]any {
	changed := map[string]any{}
	for _, field := range object.Fields {
		if field.Type != "relation" {
			continue
		}
		before, beforeExists := beforeData[field.Key]
		next, nextExists := nextData[field.Key]
		if beforeExists == nextExists && reflect.DeepEqual(before, next) {
			continue
		}
		if nextExists {
			changed[field.Key] = next
		}
	}
	return changed
}

func recordUpdateErrorFrom(kind apperror.ErrorKind, err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if errors.As(err, &coded) && strings.TrimSpace(coded.ErrorCode()) != "" {
		return &apperror.AppError{Kind: kind, Code: strings.TrimSpace(coded.ErrorCode()), Params: coded.ErrorParams(), Err: err}
	}
	code := "backend.bad_request"
	if kind == apperror.KindForbidden {
		code = "backend.forbidden"
	}
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}

func recordUpdateError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func recordUpdateInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}
