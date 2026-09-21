package validation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordFilterExpressionNormalizesTypedTreeAndProjection(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "work_item", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "priority", Type: "integer"}, {Key: "amount", Type: "currency", Config: map[string]any{"precision": 10, "scale": 2}}}}
	expression := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "status", Value: " ready "},
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{{Operator: "gte", Field: "priority", Value: "10"}, {Operator: "in", Field: "amount", Values: []any{"1", "2.30"}}}},
	}}
	normalized, err := RecordNormalizeFilterExpression(object, expression)
	if err != nil || normalized.Children[0].Value != "ready" || normalized.Children[1].Children[0].Value != int64(10) || normalized.Children[1].Children[1].Values[0] != "1.00" {
		t.Fatalf("normalized=%#v err=%v", normalized, err)
	}
	fields, err := RecordNormalizeSelectFields(object, []string{"status", "id", "status"})
	if err != nil || len(fields) != 2 || fields[0] != "status" || fields[1] != "id" {
		t.Fatalf("fields=%#v err=%v", fields, err)
	}
}

func TestRecordFilterExpressionFailsClosed(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "work_item", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}}}
	tests := []recordmodel.RecordFilterExpression{
		{Operator: "and", Children: []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "status", Value: "ready"}}},
		{Operator: "eq", Field: "missing", Value: "x"},
		{Operator: "eq", Field: "status"},
		{Operator: "in", Field: "status"},
		{Operator: "unknown", Field: "status", Value: "x"},
	}
	for _, expression := range tests {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err == nil {
			t.Fatalf("accepted invalid expression %#v", expression)
		}
	}
	deep := recordmodel.RecordFilterExpression{Operator: "not"}
	cursor := &deep
	for range 18 {
		cursor.Children = []recordmodel.RecordFilterExpression{{Operator: "not"}}
		cursor = &cursor.Children[0]
	}
	cursor.Children = []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "status", Value: "ready"}}
	if _, err := RecordNormalizeFilterExpression(object, &deep); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("deep filter err=%v", err)
	}
	if _, err := RecordNormalizeSelectFields(object, []string{"missing"}); err == nil {
		t.Fatal("unknown projection field accepted")
	}
}

func TestRecordFileFieldsOnlySupportNullFiltersAndAreNotSearchableOrSortable(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "document", Fields: []definitionmodel.FieldSchema{
		{Key: "attachment", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": false}},
		{Key: "attachments", Type: recordmodel.RecordFileListFieldType, Config: map[string]any{"scan_required": false}},
		{Key: "title", Type: "text"},
	}}
	for _, expression := range []recordmodel.RecordFilterExpression{
		{Operator: "is_null", Field: "attachment"},
		{Operator: "is_not_null", Field: "attachments"},
	} {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err != nil {
			t.Fatalf("null file filter rejected: %v", err)
		}
	}
	for _, expression := range []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "attachment", Value: "file-1"},
		{Operator: "in", Field: "attachments", Values: []any{"file-1"}},
	} {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err == nil {
			t.Fatalf("file comparison accepted: %#v", expression)
		}
	}
	query := RecordNormalizeListQuery(object, recordmodel.RecordListQuery{
		SearchFields: []string{"attachment", "title"},
		Sort:         []recordmodel.RecordSortRule{{Field: "attachments", Direction: "asc"}, {Field: "title", Direction: "desc"}},
	})
	if !reflect.DeepEqual(query.SearchFields, []string{"title"}) || !reflect.DeepEqual(query.Sort, []recordmodel.RecordSortRule{{Field: "title", Direction: "desc"}, {Field: "id", Direction: "asc"}}) {
		t.Fatalf("normalized file query=%#v", query)
	}
	err := RecordValidateListFilters(object, map[string]any{"attachment": "file-1"})
	var coded *apperror.CodedError
	if !errors.As(err, &coded) || coded.Code != "backend.validation.file_filter_unsupported" {
		t.Fatalf("legacy file filter err=%v", err)
	}
}

func TestStructuredFieldsPublishPortableQuerySemantics(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "item", Fields: []definitionmodel.FieldSchema{
		{Key: "tags", Type: recordmodel.RecordMultiSelectFieldType, Validation: definitionmodel.FieldValidation{Options: []string{"a", "b", "c"}}},
		{Key: "payload", Type: recordmodel.RecordJSONFieldType},
		{Key: "title", Type: "text"},
	}}
	for _, expression := range []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "tags", Value: []any{"b", "a", "b"}},
		{Operator: "ne", Field: "tags", Value: []any{"a"}},
		{Operator: "in", Field: "tags", Values: []any{[]any{"a"}, []any{"c", "b"}}},
		{Operator: "is_null", Field: "payload"},
		{Operator: "is_not_null", Field: "tags"},
	} {
		normalized, err := RecordNormalizeFilterExpression(object, &expression)
		if err != nil {
			t.Fatalf("valid structured expression %#v: %v", expression, err)
		}
		if expression.Operator == "eq" && !reflect.DeepEqual(normalized.Value, []string{"a", "b"}) {
			t.Fatalf("multi set=%#v", normalized.Value)
		}
	}
	for _, expression := range []recordmodel.RecordFilterExpression{
		{Operator: "gt", Field: "tags", Value: []any{"a"}},
		{Operator: "contains", Field: "tags", Value: "a"},
		{Operator: "eq", Field: "payload", Value: map[string]any{"a": 1}},
		{Operator: "in", Field: "payload", Values: []any{map[string]any{"a": 1}}},
	} {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err == nil {
			t.Fatalf("unsupported structured expression accepted: %#v", expression)
		}
	}
	query := RecordNormalizeListQuery(object, recordmodel.RecordListQuery{
		SearchFields: []string{"tags", "payload", "title"},
		Sort:         []recordmodel.RecordSortRule{{Field: "tags"}, {Field: "payload"}, {Field: "title"}},
	})
	if !reflect.DeepEqual(query.SearchFields, []string{"title"}) || !reflect.DeepEqual(query.Sort, []recordmodel.RecordSortRule{{Field: "title", Direction: "asc"}, {Field: "id", Direction: "asc"}}) {
		t.Fatalf("structured query=%#v", query)
	}
	if err := RecordValidateListFilters(object, map[string]any{"tags": []any{"a"}, "tags__in": []any{[]any{"b"}}}); err != nil {
		t.Fatalf("portable multi filters rejected: %v", err)
	}
	for filters, code := range map[string]string{
		"tags__gte": "backend.validation.multi_select_filter_unsupported",
		"payload":   "backend.validation.json_filter_unsupported",
	} {
		err := RecordValidateListFilters(object, map[string]any{filters: "value"})
		var coded *apperror.CodedError
		if !errors.As(err, &coded) || coded.Code != code {
			t.Fatalf("filters=%s err=%v", filters, err)
		}
	}
}

func TestRecordFilterExpressionCoversGenericOperatorContracts(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "item", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "count", Type: "integer"},
		{Key: "", Type: "text"},
	}}
	if normalized, err := RecordNormalizeFilterExpression(object, nil); err != nil || normalized != nil {
		t.Fatalf("nil expression normalized=%#v err=%v", normalized, err)
	}
	valid := []recordmodel.RecordFilterExpression{
		{Operator: "not", Children: []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "status", Value: "ready"}}},
		{Operator: "ne", Field: "status", Value: "ready"},
		{Operator: "gt", Field: "count", Value: 1},
		{Operator: "lt", Field: "count", Value: 2},
		{Operator: "lte", Field: "count", Value: 3},
		{Operator: "not_in", Field: "status", Values: []any{"ready"}},
		{Operator: "is_null", Field: "status"},
		{Operator: "is_not_null", Field: "status"},
		{Operator: "eq", Field: "id", Value: 7},
		{Operator: "eq", Field: "created_at", Value: " now "},
		{Operator: "eq", Field: "updated_at", Value: "later"},
	}
	for _, expression := range valid {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err != nil {
			t.Fatalf("valid expression %#v: %v", expression, err)
		}
	}

	tooMany := make([]any, recordFilterMaximumValues+1)
	for index := range tooMany {
		tooMany[index] = "value"
	}
	invalid := []recordmodel.RecordFilterExpression{
		{Operator: "and", Field: "status", Children: twoRecordFilterLeaves()},
		{Operator: "and", Value: "x", Children: twoRecordFilterLeaves()},
		{Operator: "and", Values: []any{"x"}, Children: twoRecordFilterLeaves()},
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "status", Value: "ready"}}},
		{Operator: "not", Field: "status", Children: oneRecordFilterLeaf()},
		{Operator: "not", Value: "x", Children: oneRecordFilterLeaf()},
		{Operator: "not", Values: []any{"x"}, Children: oneRecordFilterLeaf()},
		{Operator: "not", Children: twoRecordFilterLeaves()},
		{Operator: "eq", Field: "status", Value: "x", Children: oneRecordFilterLeaf()},
		{Operator: "eq", Field: "status", Value: "x", Values: []any{"x"}},
		{Operator: "eq", Field: "count", Value: "not-an-integer"},
		{Operator: "in", Field: "missing", Values: []any{"x"}},
		{Operator: "in", Field: "status", Values: []any{"x"}, Children: oneRecordFilterLeaf()},
		{Operator: "in", Field: "status", Value: "x", Values: []any{"x"}},
		{Operator: "in", Field: "status"},
		{Operator: "in", Field: "status", Values: tooMany},
		{Operator: "in", Field: "count", Values: []any{"not-an-integer"}},
		{Operator: "is_null", Field: "missing"},
		{Operator: "is_null", Field: "status", Children: oneRecordFilterLeaf()},
		{Operator: "is_null", Field: "status", Value: "x"},
		{Operator: "is_null", Field: "status", Values: []any{"x"}},
		{Operator: "eq", Field: "id", Value: " "},
		{Operator: "eq", Field: " ", Value: "x"},
	}
	for _, expression := range invalid {
		if _, err := RecordNormalizeFilterExpression(object, &expression); err == nil {
			t.Fatalf("accepted invalid expression %#v", expression)
		}
	}

	wide := recordFilterTree(7)
	if _, err := RecordNormalizeFilterExpression(object, &wide); err == nil || !strings.Contains(err.Error(), "node count") {
		t.Fatalf("wide filter err=%v", err)
	}
	if _, err := RecordNormalizeSelectFields(object, nil); err != nil {
		t.Fatalf("empty projection err=%v", err)
	}
	if _, err := RecordNormalizeSelectFields(object, []string{" "}); err == nil {
		t.Fatal("blank projection accepted")
	}
}

func oneRecordFilterLeaf() []recordmodel.RecordFilterExpression {
	return []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "status", Value: "ready"}}
}

func twoRecordFilterLeaves() []recordmodel.RecordFilterExpression {
	return append(oneRecordFilterLeaf(), oneRecordFilterLeaf()...)
}

func recordFilterTree(depth int) recordmodel.RecordFilterExpression {
	if depth == 0 {
		return recordmodel.RecordFilterExpression{Operator: "eq", Field: "status", Value: "ready"}
	}
	child := recordFilterTree(depth - 1)
	return recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{child, child}}
}
