package validation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestDictionaryValidationReturnsIndexedDuplicateFieldPath(t *testing.T) {
	payload := json.RawMessage(`{"key":"customer_status","items":[{"key":"active","value":"active"},{"key":"active","value":"enabled"}]}`)
	err := ApplicationSchemaValidateDictionaryDefinition("customer_status", payload)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.ErrorParams()["field_path"] != "items[1].key" {
		t.Fatalf("error=%v", err)
	}
}

func TestDictionaryValidationReturnsIndexedParentFieldPath(t *testing.T) {
	payload := json.RawMessage(`{"key":"region","items":[{"key":"north","value":"north","parent_key":"missing"}]}`)
	err := ApplicationSchemaValidateDictionaryDefinition("region", payload)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.ErrorParams()["field_path"] != "items[0].parent_key" || appErr.ErrorParams()["parent_key"] != "missing" {
		t.Fatalf("error=%v", err)
	}
}

func TestDictionaryNormalizationMaterializesRouteKeyAndRejectsUnknownFields(t *testing.T) {
	normalized, err := ApplicationSchemaNormalizeDictionaryDefinition("status", json.RawMessage(`{"items":[{"key":"open","value":"open"}]}`))
	if err != nil || string(normalized) != `{"key":"status","items":[{"key":"open","value":"open"}]}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	if _, err := ApplicationSchemaNormalizeDictionaryDefinition("status", json.RawMessage(`{"items":[],"unknown":true}`)); apperror.CodeOf(err) != "backend.dictionary.definition_invalid" {
		t.Fatalf("unknown field err=%v", err)
	}
}
