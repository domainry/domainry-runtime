package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const (
	recordFilterMaximumDepth  = 16
	recordFilterMaximumNodes  = 100
	recordFilterMaximumValues = 1000
)

func RecordNormalizeFilterExpression(object definitionmodel.ObjectSchema, expression *recordmodel.RecordFilterExpression) (*recordmodel.RecordFilterExpression, error) {
	if expression == nil {
		return nil, nil
	}
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields)+3)
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	for _, key := range []string{"id", "created_at", "updated_at"} {
		fields[key] = definitionmodel.FieldSchema{Key: key, Type: "text"}
	}
	nodes := 0
	normalized, err := normalizeRecordFilterNode(*expression, fields, "$", 0, &nodes)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func normalizeRecordFilterNode(expression recordmodel.RecordFilterExpression, fields map[string]definitionmodel.FieldSchema, path string, depth int, nodes *int) (recordmodel.RecordFilterExpression, error) {
	*nodes = *nodes + 1
	if depth > recordFilterMaximumDepth {
		return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s exceeds maximum depth %d", path, recordFilterMaximumDepth)
	}
	if *nodes > recordFilterMaximumNodes {
		return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter exceeds maximum node count %d", recordFilterMaximumNodes)
	}
	expression.Operator = strings.TrimSpace(strings.ToLower(expression.Operator))
	switch expression.Operator {
	case "and", "or":
		if expression.Field != "" || expression.Value != nil || len(expression.Values) != 0 || len(expression.Children) < 2 {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s %s requires at least two children and no comparison fields", path, expression.Operator)
		}
	case "not":
		if expression.Field != "" || expression.Value != nil || len(expression.Values) != 0 || len(expression.Children) != 1 {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s not requires exactly one child and no comparison fields", path)
		}
	case "eq", "ne", "gt", "gte", "lt", "lte":
		field, err := recordFilterField(fields, expression.Field, path)
		if err != nil {
			return recordmodel.RecordFilterExpression{}, err
		}
		if len(expression.Children) != 0 || len(expression.Values) != 0 || expression.Value == nil {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s %s requires one non-null value", path, expression.Operator)
		}
		expression.Field = field.Key
		expression.Value, err = normalizeRecordFilterValue(field, expression.Value)
		if err != nil {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s value: %w", path, err)
		}
	case "in", "not_in":
		field, err := recordFilterField(fields, expression.Field, path)
		if err != nil {
			return recordmodel.RecordFilterExpression{}, err
		}
		if len(expression.Children) != 0 || expression.Value != nil || len(expression.Values) == 0 || len(expression.Values) > recordFilterMaximumValues {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s %s requires 1..%d values", path, expression.Operator, recordFilterMaximumValues)
		}
		expression.Field = field.Key
		for index, value := range expression.Values {
			expression.Values[index], err = normalizeRecordFilterValue(field, value)
			if err != nil {
				return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s values[%d]: %w", path, index, err)
			}
		}
	case "is_null", "is_not_null":
		field, err := recordFilterField(fields, expression.Field, path)
		if err != nil {
			return recordmodel.RecordFilterExpression{}, err
		}
		if len(expression.Children) != 0 || expression.Value != nil || len(expression.Values) != 0 {
			return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s %s does not accept values or children", path, expression.Operator)
		}
		expression.Field = field.Key
	default:
		return recordmodel.RecordFilterExpression{}, fmt.Errorf("record filter %s has unsupported operator %q", path, expression.Operator)
	}
	if expression.Operator == "and" || expression.Operator == "or" || expression.Operator == "not" {
		for index := range expression.Children {
			normalized, err := normalizeRecordFilterNode(expression.Children[index], fields, fmt.Sprintf("%s.children[%d]", path, index), depth+1, nodes)
			if err != nil {
				return recordmodel.RecordFilterExpression{}, err
			}
			expression.Children[index] = normalized
		}
	}
	return expression, nil
}

func recordFilterField(fields map[string]definitionmodel.FieldSchema, raw, path string) (definitionmodel.FieldSchema, error) {
	key := strings.TrimSpace(raw)
	field, ok := fields[key]
	if !ok || key == "" {
		return definitionmodel.FieldSchema{}, fmt.Errorf("record filter %s references unknown field %q", path, key)
	}
	return field, nil
}

func normalizeRecordFilterValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if field.Key == "id" || field.Key == "created_at" || field.Key == "updated_at" {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return nil, fmt.Errorf("value is empty")
		}
		return text, nil
	}
	return RecordNormalizeFieldValue(field, value)
}

func RecordNormalizeSelectFields(object definitionmodel.ObjectSchema, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{"id": true, "created_at": true, "updated_at": true}
	for _, field := range object.Fields {
		allowed[strings.TrimSpace(field.Key)] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for index, raw := range values {
		key := strings.TrimSpace(raw)
		if key == "" || !allowed[key] {
			return nil, fmt.Errorf("record select_fields[%d] references unknown field %q", index, key)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result, nil
}
