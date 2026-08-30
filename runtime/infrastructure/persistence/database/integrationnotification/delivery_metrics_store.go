package integrationnotification

import (
	"database/sql"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// DeliveryMetricsStore projects Runtime-owned Integration Outbox delivery
// evidence into the Notification SDK metrics response. It owns no Notification
// tables or lifecycle behavior.
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
