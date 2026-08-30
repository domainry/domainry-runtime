package dataset

import (
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

// Row is the authorized, alias-addressed record tuple consumed by Dataset
// joins, filters, analyses, and aggregation.
type ReportDatasetRow map[string]*recordmodel.Record

func CopyRow(row ReportDatasetRow) ReportDatasetRow {
	copyRow := make(ReportDatasetRow, len(row)+1)
	for alias, record := range row {
		copyRow[alias] = record
	}
	return copyRow
}

func FieldValue(row ReportDatasetRow, field reportmodel.ReportDatasetField) (any, bool) {
	return RecordPointerValue(row[strings.TrimSpace(field.SourceAlias)], field.FieldKey)
}

func RecordPointerValue(record *recordmodel.Record, field string) (any, bool) {
	if record == nil {
		return nil, false
	}
	return RecordValue(*record, field)
}

func RecordValue(record recordmodel.Record, field string) (any, bool) {
	switch strings.TrimSpace(field) {
	case "id":
		return record.ID, record.ID != ""
	case "created_at":
		return record.CreatedAt, record.CreatedAt != ""
	case "updated_at":
		return record.UpdatedAt, record.UpdatedAt != ""
	default:
		value, ok := record.Data[strings.TrimSpace(field)]
		return value, ok
	}
}
