package service

import (
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func notificationBadRequest(code string, params ...string) error {
	return notificationError(apperror.KindBadRequest, code, params...)
}

func notificationNotFound(code string, params ...string) error {
	return notificationError(apperror.KindNotFound, code, params...)
}

func notificationConflict(code string, params ...string) error {
	return notificationError(apperror.KindConflict, code, params...)
}

func notificationError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values}
}
