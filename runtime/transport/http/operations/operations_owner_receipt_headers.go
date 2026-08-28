package operations

import (
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

func WriteOwnerReceiptHeaders(w http.ResponseWriter, result operationsapplication.OperationsOwnerExecutionResult) {
	if strings.TrimSpace(result.Receipt.Command.ID) == "" {
		return
	}
	w.Header().Set("Operation-ID", result.Receipt.Command.ID)
	w.Header().Set("Operation-Location", result.Receipt.StatusURL)
	w.Header().Set("Location", result.Receipt.StatusURL)
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
}

func OwnerOperationReason(r *http.Request, fallback string) string {
	if r != nil {
		if reason := strings.TrimSpace(r.Header.Get("X-Operation-Reason")); reason != "" {
			return reason
		}
	}
	return fallback
}
