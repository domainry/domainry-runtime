package agent

import (
	"errors"

	"github.com/domainry/domainry-foundation/apperror"
)

func errorKind(err error) apperror.ErrorKind {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return apperror.KindInternal
}
