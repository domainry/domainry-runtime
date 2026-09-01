package contract

import (
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report-sdk/query"
	reportobjectsql "github.com/domainry/domainry-report-sdk/query/objectsql"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
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
			metadata := reportengine.Field{Key: field.Key, Type: field.Type, Scale: -1}
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
