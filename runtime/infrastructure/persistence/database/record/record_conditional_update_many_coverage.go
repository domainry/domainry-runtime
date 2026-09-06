package record

import (
	"encoding/json"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (r RecordStore) conditionalUpdateManyCoverageDBValues(commit transactionmodel.RecordMutationCommit) (string, []any, error) {
	fieldCatalog := map[string]definitionmodel.FieldSchema{"id": {Key: "id", Type: "text"}}
	for _, field := range commit.Object.Fields {
		fieldCatalog[field.Key] = field
	}
	fieldKey := strings.TrimSpace(commit.SetExactCoverageField)
	field, ok := fieldCatalog[fieldKey]
	if !ok || fieldKey == "" || fieldKey != "id" && recordFieldIsSystemOwned(fieldKey) {
		return "", nil, fmt.Errorf("conditional update-many exact coverage field is invalid")
	}
	values := make([]any, len(commit.SetExactCoverageValues))
	seen := map[string]bool{}
	for index, value := range commit.SetExactCoverageValues {
		normalized, err := normalizeConditionalUpdateManyCoverageDBValue(field, value)
		if err != nil {
			return "", nil, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return "", nil, fmt.Errorf("encode conditional update-many exact coverage value: %w", err)
		}
		identity := fmt.Sprintf("%T:%s", normalized, encoded)
		if seen[identity] {
			return "", nil, fmt.Errorf("conditional update-many exact coverage values are not distinct")
		}
		seen[identity] = true
		values[index] = dbFieldValue(r.store.RuntimeEngine, field, normalized)
	}
	return fieldKey, values, nil
}

func normalizeConditionalUpdateManyCoverageDBValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if field.Key == "id" {
		normalized := strings.TrimSpace(fmt.Sprint(value))
		if value == nil || normalized == "" {
			return nil, fmt.Errorf("conditional update-many exact coverage value is invalid")
		}
		return normalized, nil
	}
	normalized, err := recordvalidation.RecordNormalizeFieldValue(field, value)
	if err != nil || normalized == nil {
		return nil, fmt.Errorf("conditional update-many exact coverage value is invalid: %w", err)
	}
	return normalized, nil
}
