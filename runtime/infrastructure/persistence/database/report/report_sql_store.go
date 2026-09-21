package report

import (
	"context"
	"database/sql"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ reportcontract.ReportSnapshotSourceVersionReader = (*ReportSQLStore)(nil)

type ReportSQLStore struct {
	store   *database.RuntimeStore
	beginTx func(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func NewReportSQLStore(store *database.RuntimeStore) *ReportSQLStore {
	result := &ReportSQLStore{store: store}
	if store != nil && store.DB() != nil {
		result.beginTx = store.DB().BeginTx
	}
	return result
}

func (s *ReportSQLStore) ReadReportSnapshotSourceVersion(ctx context.Context, request reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	// Governed query evidence must be invalidated by every change to the
	// effective, permission-scoped source projection. A row count plus
	// MAX(updated_at) misses updates to non-latest rows and timestamp
	// collisions, so both report evidence and analysis use the same stable
	// content fingerprint implementation.
	return s.ReadReportAnalysisSourceVersion(ctx, request)
}
