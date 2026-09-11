package validation

import (
	"errors"
	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordNormalizeListQueryIdentityEqualityFilter(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "lead", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	query := RecordNormalizeListQuery(object, recordmodel.RecordListQuery{Filters: map[string]any{"id": " lead_1 "}})
	values, ok := query.Filters["id__in"].([]any)
	if !ok || len(values) != 1 || values[0] != "lead_1" {
		t.Fatalf("id equality must normalize to id__in: %v", query.Filters)
	}
	if _, present := query.Filters["id"]; present {
		t.Fatalf("raw id key must not survive normalization: %v", query.Filters)
	}
}

func TestRecordValidateListFiltersRefusesUnknownKeys(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "lead", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	for _, accepted := range []map[string]any{
		{"id": "lead_1"}, {"id__in": []any{"lead_1"}}, {"status": "open"}, {"status__in": []any{"open"}}, {"workspace_id": "ws"}, {"": "x"}, nil,
	} {
		if err := RecordValidateListFilters(object, accepted); err != nil {
			t.Fatalf("filters %v must be accepted: %v", accepted, err)
		}
	}
	err := RecordValidateListFilters(object, map[string]any{"status": "open", "owner": "u1"})
	var appErr *apperror.CodedError
	if !errors.As(err, &appErr) || appErr.Code != "backend.validation.filter_field_unknown" || appErr.Params["field"] != "owner" || appErr.Params["object_key"] != "lead" {
		t.Fatalf("unknown filter key must be refused by name: %v", err)
	}
	if err := RecordValidateListFilters(object, map[string]any{"owner__gte": 1}); err == nil {
		t.Fatal("operator suffix on an unknown key must still be refused")
	}
}
