package contract

import (
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report-sdk/query"
	reportobjectsql "github.com/domainry/domainry-report-sdk/query/objectsql"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

// CompileReportObjectSQL is the Runtime compatibility adapter for the
// Report-owned compiler. Runtime projects only active object metadata into the
// deployment-neutral Report engine shape.
func CompileReportObjectSQL(schema reportmodel.ReportObjectSQLSchema, objects map[string]definitionmodel.ObjectSchema) (reportmodel.ReportObjectSQLPlan, error) {
	projected, err := ReportEngineObjects(objects)
	if err != nil {
		return reportmodel.ReportObjectSQLPlan{}, err
	}
	return reportobjectsql.CompileReportObjectSQL(schema, projected)
}

// CanonicalReportObjectSQL returns the publication contract derived by the
// Report compiler while keeping Runtime ObjectSchema behind this adapter.
func CanonicalReportObjectSQL(schema reportmodel.ReportObjectSQLSchema, objects map[string]definitionmodel.ObjectSchema) (reportmodel.ReportObjectSQLSchema, reportmodel.ReportObjectSQLPlan, error) {
	projected, err := ReportEngineObjects(objects)
	if err != nil {
		return reportmodel.ReportObjectSQLSchema{}, reportmodel.ReportObjectSQLPlan{}, err
	}
	return reportobjectsql.CanonicalReportObjectSQL(schema, projected)
}

// ReportObjectSQLSourceObjects discovers SQL-owned source dependencies when a
// canonical or legacy source assertion is not present yet.
func ReportObjectSQLSourceObjects(schema reportmodel.ReportObjectSQLSchema) ([]string, error) {
	if len(schema.SourceObjects) > 0 {
		return append([]string(nil), schema.SourceObjects...), nil
	}
	return reportobjectsql.DiscoverReportObjectSQLSources(schema.SQL)
}

// ReportEngineObjects is the single Runtime-to-Report object metadata
// projection. Invalid decimal metadata fails closed for every Report path.
func ReportEngineObjects(objects map[string]definitionmodel.ObjectSchema) (map[string]reportengine.Object, error) {
	projected := make(map[string]reportengine.Object, len(objects))
	for alias, object := range objects {
		fields := make([]reportengine.Field, 0, len(object.Fields))
		for _, field := range object.Fields {
			if strings.TrimSpace(field.DisabledAt) != "" {
				continue
			}
			metadata := reportengine.Field{
				Key: field.Key, Type: field.Type, Scale: -1, Unique: field.Unique,
			}
			if strings.TrimSpace(field.Type) == "relation" {
				metadata.RelationTarget = recordvalidation.RecordRelationTarget(field)
				metadata.RelationCardinality = recordvalidation.RecordConfigString(field.Config["cardinality"])
				if metadata.RelationCardinality == "" {
					metadata.RelationCardinality = "many_to_one"
				}
			}
			switch strings.TrimSpace(field.Type) {
			case "currency", "decimal", "percent":
				config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
				if err != nil {
					return nil, err
				}
				metadata.Precision, metadata.Scale = config.Precision, config.Scale
			}
			fields = append(fields, metadata)
		}
		projected[alias] = reportengine.Object{Key: object.Key, Fields: fields}
	}
	return projected, nil
}
