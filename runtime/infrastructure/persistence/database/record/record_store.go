package record

import (
	"context"
	"database/sql"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

// RecordStore is the ctx-first record aggregate view of database.Store. The
// legacy database.Store methods remain during migration, but new services should receive
// this bounded view instead of the full storage object.
type RecordStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewRecordStore(store *database.RuntimeStore) RecordStore {
	return RecordStore{store: store}
}

func (r RecordStore) database() *sql.DB {
	if r.db != nil {
		return r.db
	}
	if r.store == nil {
		return nil
	}
	return r.store.DB()
}
func (r RecordStore) queryExecutor(ctx context.Context) recordQueryExecutor {
	if tx := actionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return r.database()
}

type recordQueryExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func recordScopeReadTxOptions(profile persistencedriver.EngineProfile) *sql.TxOptions {
	return &sql.TxOptions{Isolation: profile.RecordReadIsolation(), ReadOnly: true}
}
