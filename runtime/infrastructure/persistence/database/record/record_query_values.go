package record

import (
	ormbuilder "github.com/domainry/domainry-orm/query"
	recordschema "github.com/domainry/domainry-orm/recordschema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"

	"strings"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func recordQueryDBValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	if !profile.OrderedDecimalTextStorage() {
		return query
	}
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	filters := make(map[string]any, len(query.Filters))
	for key, value := range query.Filters {
		fieldKey := key
		if index := strings.LastIndex(fieldKey, "__"); index > 0 {
			fieldKey = fieldKey[:index]
		}
		field, ok := fields[fieldKey]
		if !ok || field.Type != "currency" {
			filters[key] = value
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
	query.Filters = filters
	if query.FilterExpression != nil {
		encoded := recordFilterDBValues(profile, fields, *query.FilterExpression)
		query.FilterExpression = &encoded
	}
	return query
}

// RecordQueryDatabaseValues converts canonical query values to the physical
// representation used by a database-backed Record table.
func RecordQueryDatabaseValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	return recordQueryDBValues(profile, object, query)
}

func recordFilterDBValues(profile persistencedriver.EngineProfile, fields map[string]definitionmodel.FieldSchema, expression recordmodel.RecordFilterExpression) recordmodel.RecordFilterExpression {
	field, isCurrency := fields[expression.Field]
	isCurrency = isCurrency && field.Type == "currency"
	if isCurrency && expression.Value != nil {
		expression.Value = dbFieldValue(profile, field, expression.Value)
	}
	if isCurrency {
		for index, value := range expression.Values {
			expression.Values[index] = dbFieldValue(profile, field, value)
		}
	}
	for index := range expression.Children {
		expression.Children[index] = recordFilterDBValues(profile, fields, expression.Children[index])
	}
	return expression
}

func recordListProjections(selectFields []string) []ormbuilder.Projection {
	if len(selectFields) == 0 {
		return []ormbuilder.Projection{ormbuilder.Project(ormbuilder.Star())}
	}
	fields := recordschema.SystemColumnNames()
	seen := make(map[string]bool, len(fields)+len(selectFields))
	projections := make([]ormbuilder.Projection, 0, len(fields)+len(selectFields))
	for _, field := range fields {
		seen[field] = true
		projections = append(projections, ormbuilder.Project(ormbuilder.Column(field)))
	}
	for _, field := range selectFields {
		if !seen[field] {
			seen[field] = true
			projections = append(projections, ormbuilder.Project(ormbuilder.Column(field)))
		}
	}
	return projections
}

// ApplyRecordMutationTx lets the workflow decision adapter participate in the
// same SQL transaction without moving record mutation rules back to database.
