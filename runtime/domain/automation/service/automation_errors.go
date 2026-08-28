package service

import (
	"errors"

	"github.com/domainry/domainry-foundation/apperror"
)

func automationErrorDetails(err error) (string, map[string]string) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode(), appErr.ErrorParams()
	}
	return "backend.internal", nil
}
