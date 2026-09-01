package records

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
)

func recordsActionIdempotencyKey(request *http.Request) (string, error) {
	headerKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if headerKey != "" {
		return headerKey, nil
	}
	return "", apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "action.invoke"})
}

func (h *RecordsHandler) writeActionServiceError(writer http.ResponseWriter, request *http.Request, err error) {
	if apperror.CodeOf(err) == idempotency.ErrorCodeInProgress {
		retryAfter := strings.TrimSpace(apperror.ParamsOf(err)["retry_after"])
		if retryAfter == "" {
			retryAfter = "1"
		}
		writer.Header().Set("Retry-After", retryAfter)
	}
	h.writeServiceError(writer, request, err)
}

func recordsMarkActionReplay(writer http.ResponseWriter, message string) {
	if strings.TrimSpace(message) == "backend.action.idempotent_replay" {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
}
