package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func recordStringListFromAny(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		result = append(result, typed...)
	case []any:
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				result = append(result, text)
			}
		}
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func recordValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

// recordInternalError classifies a repository failure as backend.internal --
// unless the repository already classified it. The record store raises client
// errors with stable codes (a keyset page requested without its cursor is
// backend.record.pagination_cursor_required, KindBadRequest); wrapping those
// into KindInternal here turned them back into the 500 with
// {"operation":"list records"} that the store-level fix was meant to end, and
// a delivery reproduced exactly that 500 on page 2 of every Object. A
// classified non-internal error passes through unchanged.
func recordInternalError(operation string, err error) error {
	var classified *apperror.AppError
	if errors.As(err, &classified) && classified != nil && classified.Kind != apperror.KindInternal && classified.Kind != "" {
		return err
	}
	// A CodedError is the other half of the same story: the foundation defines it
	// as a stable client-facing code raised at a leaf boundary, for the
	// Application layer to give a kind. Burying it here published
	// backend.validation.filter_field_unknown -- a documented 400 naming the bad
	// filter key -- as a 500 backend.internal with the key thrown away.
	var coded *apperror.CodedError
	if errors.As(err, &coded) && coded != nil && strings.TrimSpace(coded.Code) != "" {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: coded.Code, Params: coded.ErrorParams()}
	}
	return recordServiceError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordServiceError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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

func recordStateMachineError(kind apperror.ErrorKind, code string, params ...string) error {
	return recordServiceError(kind, code, nil, params...)
}
