package service

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
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

func recordInternalError(operation string, err error) error {
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
