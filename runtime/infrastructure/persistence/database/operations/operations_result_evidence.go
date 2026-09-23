package operations

import (
	"encoding/json"
	"fmt"

	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
)

func inlineOperationsResult(result json.RawMessage) (json.RawMessage, error) {
	redacted, err := operationspolicy.OperationsRedactResult(result)
	if err != nil {
		return nil, err
	}
	if len(redacted) > operationspolicy.OperationsMaximumInlineResultBytes {
		return nil, fmt.Errorf("operation result exceeds inline evidence limit; store it as a shared Artifact and complete with a bounded reference")
	}
	return redacted, nil
}
