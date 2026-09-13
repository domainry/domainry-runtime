package repository

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// RecordSubjectErasureRepository applies the prepared business mutations in
// one host-database transaction without copying original personal values into
// record audit rows. Lifecycle records the governed request and its outcome.
type RecordSubjectErasureRepository interface {
	ApplySubjectErasure(context.Context, string, []definitionmodel.ObjectSchema, []recordmodel.SubjectErasureMutation) error
}
