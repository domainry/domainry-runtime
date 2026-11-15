package workflows

import (
	"bytes"
	"encoding/json"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

// Operations replays its durable JSON result, not the original Go value.
// Restore only this endpoint's declared DTO after the owner readiness check;
// never re-execute the mutation to obtain a typed response.
func workflowOperationValue[T any](operation operationsapplication.OperationsOwnerExecutionResult) (T, bool) {
	if value, ok := operation.Value.(T); ok {
		return value, true
	}
	var value T
	if !operation.Replayed || operation.Value == nil {
		return value, false
	}
	raw, err := json.Marshal(operation.Value)
	if err != nil || len(raw) > 1<<20 {
		return value, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, false
	}
	return value, true
}
