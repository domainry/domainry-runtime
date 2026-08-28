package validation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestDictionaryValidationReturnsIndexedDuplicateFieldPath(t *testing.T) {
	payload := json.RawMessage(`{"key":"customer_status","items":[{"key":"active","value":"active"},{"key":"active","value":"enabled"}]}`)
	err := MetadataValidateDictionaryDefinition("customer_status", payload)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.ErrorParams()["field_path"] != "items[1].key" {
		t.Fatalf("error=%v", err)
	}
}

func TestDictionaryValidationReturnsIndexedParentFieldPath(t *testing.T) {
	payload := json.RawMessage(`{"key":"region","items":[{"key":"north","value":"north","parent_key":"missing"}]}`)
	err := MetadataValidateDictionaryDefinition("region", payload)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.ErrorParams()["field_path"] != "items[0].parent_key" || appErr.ErrorParams()["parent_key"] != "missing" {
		t.Fatalf("error=%v", err)
	}
}
