package notification

import (
	"database/sql"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// DeliveryMetricsStore adapts module-owned delivery tables to Plane's metrics
// response model. It owns no notification lifecycle or mutation behavior.
type DeliveryMetricsStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewDeliveryMetricsStore(store *database.RuntimeStore) DeliveryMetricsStore {
	return DeliveryMetricsStore{store: store}
}

func (r DeliveryMetricsStore) database() *sql.DB {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}
