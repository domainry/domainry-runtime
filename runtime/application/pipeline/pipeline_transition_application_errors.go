package pipeline

import (
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func pipelineTransitionValueOr(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func pipelineTransitionClean(value any) string {
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
}

func pipelineTransitionBadRequest(err error) error {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	type coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	var value coded
	if errors.As(err, &value) {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: value.ErrorCode(), Params: value.ErrorParams(), Err: err}
	}
	return pipelineTransitionError(apperror.KindBadRequest, "backend.bad_request", err)
}

func pipelineTransitionInternal(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}

func pipelineTransitionError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
