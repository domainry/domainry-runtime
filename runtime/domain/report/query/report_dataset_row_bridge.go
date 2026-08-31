package query

import (
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report/query/engine"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportdataset "github.com/domainry/domainry-runtime/runtime/domain/report/query/dataset"
)

type reportDatasetRow reportdataset.ReportDatasetRow

func reportCopyDatasetRow(row reportDatasetRow) reportDatasetRow {
	return reportDatasetRow(reportdataset.CopyRow(reportdataset.ReportDatasetRow(row)))
}
func reportDatasetFieldValue(row reportDatasetRow, field reportmodel.ReportDatasetField) (any, bool) {
	return reportdataset.FieldValue(reportdataset.ReportDatasetRow(row), field)
}
func reportRecordPointerValue(record *recordmodel.Record, field string) (any, bool) {
	return reportdataset.RecordPointerValue(record, field)
}
func reportRecordValue(record recordmodel.Record, field string) (any, bool) {
	return reportdataset.RecordValue(record, field)
}

func reportEngineRows(rows []reportDatasetRow) []reportengine.Row {
	adapted := make([]reportengine.Row, 0, len(rows))
	for _, row := range rows {
		engineRow := make(reportengine.Row, len(row))
		for alias, record := range row {
			if record == nil {
				engineRow[alias] = nil
				continue
			}
			engineRow[alias] = &reportengine.Record{
				ID: record.ID, Data: record.Data,
				CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
			}
		}
		adapted = append(adapted, engineRow)
	}
	return adapted
}

func reportEngineRecords(records []recordmodel.Record) []reportengine.Record {
	adapted := make([]reportengine.Record, len(records))
	for index, record := range records {
		adapted[index] = reportengine.Record{ID: record.ID, Data: record.Data, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	}
	return adapted
}

func reportRuntimeRows(rows []reportengine.Row) []reportDatasetRow {
	adapted := make([]reportDatasetRow, 0, len(rows))
	for _, row := range rows {
		runtimeRow := make(reportDatasetRow, len(row))
		for alias, record := range row {
			if record == nil {
				runtimeRow[alias] = nil
				continue
			}
			runtimeRow[alias] = &recordmodel.Record{
				ID: record.ID, Data: record.Data,
				CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
			}
		}
		adapted = append(adapted, runtimeRow)
	}
	return adapted
}

func reportEngineObjects(objects map[string]definitionmodel.ObjectSchema) (map[string]reportengine.Object, error) {
	adapted := make(map[string]reportengine.Object, len(objects))
	for alias, object := range objects {
		fields := make([]reportengine.Field, 0, len(object.Fields))
		for _, field := range object.Fields {
			scale := int32(-1)
			if field.Type == "currency" || field.Type == "decimal" {
				config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
				if err != nil {
					return nil, err
				}
				scale = config.Scale
			}
			precision := 0
			if field.Type == "currency" || field.Type == "decimal" {
				config, _ := recordmodel.RecordNormalizeDecimalConfig(field.Config)
				precision = config.Precision
			}
			fields = append(fields, reportengine.Field{Key: field.Key, Type: field.Type, Precision: precision, Scale: scale})
		}
		adapted[alias] = reportengine.Object{Fields: fields}
	}
	return adapted, nil
}
