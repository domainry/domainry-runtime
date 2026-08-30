package repository

import (
	"context"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
)

type ChangePlanEvidenceRepository interface {
	ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error)
}
