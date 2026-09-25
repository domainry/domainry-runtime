package record

import (
	"github.com/domainry/domainry-orm/query"
	recordschema "github.com/domainry/domainry-orm/recordschema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"

	"strings"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func recordQueryDBValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, queryValue recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	filters := make(map[string]any, len(queryValue.Filters))
	for key, value := range queryValue.Filters {
		fieldKey := key
		if index := strings.LastIndex(fieldKey, "__"); index > 0 {
			fieldKey = fieldKey[:index]
		}
		field, ok := fields[fieldKey]
		if !ok || !recordFieldQueryValueNeedsEncoding(profile, field) {
			filters[key] = value
			continue
		}
		if recordmodel.RecordIsStructuredFieldType(field.Type) && !strings.HasSuffix(key, "__in") {
			filters[key] = dbFieldValue(profile, field, value)
			continue
		}
		switch typed := value.(type) {
		case []any:
			encoded := make([]any, 0, len(typed))
			for _, item := range typed {
				encoded = append(encoded, dbFieldValue(profile, field, item))
			}
			filters[key] = encoded
		case []string:
			encoded := make([]string, 0, len(typed))
			for _, item := range typed {
				encoded = append(encoded, fmt.Sprint(dbFieldValue(profile, field, item)))
			}
			filters[key] = encoded
		default:
			filters[key] = dbFieldValue(profile, field, value)
		}
	}
	queryValue.Filters = filters
	if queryValue.FilterExpression != nil {
		encoded := recordFilterDBValues(profile, fields, *queryValue.FilterExpression)
		queryValue.FilterExpression = &encoded
	}
	return queryValue
}

func RecordQueryDatabaseValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, queryValue recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	return recordQueryDBValues(profile, object, queryValue)
}

func recordFilterDBValues(profile persistencedriver.EngineProfile, fields map[string]definitionmodel.FieldSchema, expression recordmodel.RecordFilterExpression) recordmodel.RecordFilterExpression {
	field, needsEncoding := fields[expression.Field]
	needsEncoding = needsEncoding && recordFieldQueryValueNeedsEncoding(profile, field)
	if needsEncoding && expression.Value != nil {
		expression.Value = dbFieldValue(profile, field, expression.Value)
	}
	if needsEncoding {
		for index, value := range expression.Values {
			expression.Values[index] = dbFieldValue(profile, field, value)
		}
	}
	for index := range expression.Children {
		expression.Children[index] = recordFilterDBValues(profile, fields, expression.Children[index])
	}
	return expression
}

func recordFieldQueryValueNeedsEncoding(profile persistencedriver.EngineProfile, field definitionmodel.FieldSchema) bool {
	return strings.TrimSpace(field.Type) == "datetime" || recordmodel.RecordIsStructuredFieldType(field.Type) || profile.OrderedDecimalTextStorage() && (field.Type == "currency" || field.Type == "percent")
}

func recordListProjections(selectFields []string) []query.Projection {
	if len(selectFields) == 0 {
		return []query.Projection{query.Project(query.Star())}
	}
	fields := recordschema.SystemColumnNames()
	seen := make(map[string]bool, len(fields)+len(selectFields))
	projections := make([]query.Projection, 0, len(fields)+len(selectFields))
	for _, field := range fields {
		seen[field] = true
		projections = append(projections, query.Project(query.Column(field)))
	}
	for _, field := range selectFields {
		if !seen[field] {
			seen[field] = true
			projections = append(projections, query.Project(query.Column(field)))
		}
	}
	return projections
}
