package repository

import (
	"context"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
)

// ProvenanceRepository persists evidence tying generated rows to their seed source.
type ProvenanceRepository interface {
	UpsertSeedProvenance(context.Context, businessseedmodel.BusinessSeedProvenance) error
}
