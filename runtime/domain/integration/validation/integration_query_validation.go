package validation

import (
	"fmt"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// IntegrationValidateReadLimit validates the public Integration list contract.
// Application-internal readers may use larger bounded batches for owner
// aggregation and are not governed by this transport-facing authoring limit.
func IntegrationValidateReadLimit(limit, maximum int) error {
	if limit >= 1 && limit <= maximum {
		return nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.query_limit_invalid", Params: map[string]string{
		"minimum": "1", "maximum": fmt.Sprint(maximum), "actual": fmt.Sprint(limit),
	}}
}
