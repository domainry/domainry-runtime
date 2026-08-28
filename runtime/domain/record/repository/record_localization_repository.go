package repository

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// RecordLocalizationRepository is optional for non-SQL test doubles and
// required by locale-aware Runtime record reads.
type RecordLocalizationRepository interface {
	ListRecordLocalizedValues(context.Context, string, definitionmodel.ObjectSchema, []string, []string, []string) ([]recordmodel.RecordLocalizedValue, error)
	GetRecordLocalized(context.Context, string, definitionmodel.ObjectSchema, string, string, string) (recordmodel.Record, bool, error)
}
