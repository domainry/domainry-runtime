package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
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
	if s == nil || s.store == nil || s.store.DB() == nil {
		return reportmodel.ReportSnapshotSourceVersion{}, fmt.Errorf("report SQL store is unavailable")
	}
	tx, err := s.beginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, fmt.Errorf("begin report source version snapshot: %w", err)
	}
	defer tx.Rollback()

	aliases := make([]string, 0, len(request.Objects))
	for alias := range request.Objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	result := reportmodel.ReportSnapshotSourceVersion{SourceVersions: map[string]string{}}
	for _, alias := range aliases {
		object := request.Objects[alias]
		queryValue := request.Queries[alias]
		if queryValue.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*queryValue.ScopeExpression) {
			resolved, resolveErr := querypersistence.ResolveScopeMembership(s.store, request.WorkspaceID, *queryValue.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, lookupArgs ...any) ([]string, error) {
				rows, queryErr := tx.QueryContext(ctx, statement, lookupArgs...)
				if queryErr != nil {
					return nil, queryErr
				}
				defer rows.Close()
				values := []string{}
				for rows.Next() {
					var value string
					if scanErr := rows.Scan(&value); scanErr != nil {
						return nil, scanErr
					}
					values = append(values, value)
				}
				return values, rows.Err()
			})
			if resolveErr != nil {
				return reportmodel.ReportSnapshotSourceVersion{}, resolveErr
			}
			queryValue.ScopeExpression = &resolved
		}
		queryValue = recordpersistence.RecordQueryDatabaseValues(s.store.RuntimeEngine, object, queryValue)
		predicate, predicateErr := querypersistence.BuildTenantPredicate(s.store, request.WorkspaceID, queryValue)
		if predicateErr != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, predicateErr
		}
		statement, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, object.Key).
			Projections(query.Project(query.CountAll()), query.Project(query.Coalesce(query.Max(query.Column("updated_at")), query.Value("")))).
			Where(predicate).Build()
		if buildErr != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, buildErr
		}
		var count int64
		var watermark string
		if err := tx.QueryRowContext(ctx, statement, args...).Scan(&count, &watermark); err != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, fmt.Errorf("read report source version %s: %w", alias, err)
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s", object.Key, alias, count, watermark)))
		result.SourceVersions[alias] = fmt.Sprintf("%d:%s", count, hex.EncodeToString(sum[:]))
		if watermark > result.Watermark {
			result.Watermark = watermark
		}
	}
	if err := tx.Commit(); err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, err
	}
	return result, nil
}
