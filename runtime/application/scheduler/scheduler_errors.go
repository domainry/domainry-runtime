package scheduler

import (
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func badRequest(code string, params ...string) error {
	return schedulerError(apperror.KindBadRequest, code, nil, params...)
}
func forbidden(code string, params ...string) error {
	return schedulerError(apperror.KindForbidden, code, nil, params...)
}
func notFound(code string, params ...string) error {
	return schedulerError(apperror.KindNotFound, code, nil, params...)
}
func internalError(operation string, err error) error {
	return schedulerError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func schedulerError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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
