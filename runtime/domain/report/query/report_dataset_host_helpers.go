package query

import (
	"strings"

	reportengine "github.com/domainry/domainry-report/query/engine"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const reportDatasetPageSize = 500

// reportDatasetFieldSchema is a host adapter: object definitions remain owned
// by Runtime while calculation field metadata is projected into Report.
func reportDatasetFieldSchema(object definitionmodel.ObjectSchema, fieldKey string) definitionmodel.FieldSchema {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == strings.TrimSpace(fieldKey) {
			return field
		}
	}
	return definitionmodel.FieldSchema{Key: fieldKey, Type: "text"}
}

func reportSQLEngineObjects(objects map[string]definitionmodel.ObjectSchema) map[string]reportengine.Object {
	adapted := make(map[string]reportengine.Object, len(objects))
	for alias, object := range objects {
		fields := make([]reportengine.Field, 0, len(object.Fields))
		for _, field := range object.Fields {
			metadata := reportengine.Field{Key: field.Key, Type: field.Type, Scale: -1}
			if field.Type == "currency" || field.Type == "decimal" {
				if config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config); err == nil {
					metadata.Precision, metadata.Scale = config.Precision, config.Scale
				}
			}
			fields = append(fields, metadata)
		}
		adapted[alias] = reportengine.Object{Fields: fields}
	}
	return adapted
}
