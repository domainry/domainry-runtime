package http

import (
	"net/http"

	"github.com/domainry/domainry-foundation/apperror"
)

func serviceErrorHTTPStatus(_ *http.Request, err error) int {
	if apperror.CodeOf(err) == "auth.session_expired" {
		return http.StatusUnauthorized
	}
	status := http.StatusInternalServerError
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		status = http.StatusBadRequest
	case apperror.KindForbidden:
		status = http.StatusForbidden
	case apperror.KindNotFound:
		status = http.StatusNotFound
	case apperror.KindConflict:
		status = http.StatusConflict
	case apperror.KindRateLimited:
		status = http.StatusTooManyRequests
	case apperror.KindUnavailable:
		status = http.StatusServiceUnavailable
	}
	return status
}
