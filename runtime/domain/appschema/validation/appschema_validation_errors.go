package validation

import (
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func badRequest(code string, params ...string) error {
	return metadataValidationError(apperror.KindBadRequest, code, params...)
}

func forbidden(code string, params ...string) error {
	return metadataValidationError(apperror.KindForbidden, code, params...)
}

func metadataValidationError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values}
}
