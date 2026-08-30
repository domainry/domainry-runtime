package record

import (
	ormbuilder "github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"github.com/domainry/domainry-foundation/apperror"

	"context"
	"database/sql"
	"fmt"

	"strings"

	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
)

func (r RecordStore) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	query.FilterExpression, err = recordvalidation.RecordNormalizeFilterExpression(object, query.FilterExpression)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record filter: %w", err)
	}
	query.SelectFields, err = recordvalidation.RecordNormalizeSelectFields(object, query.SelectFields)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record projection: %w", err)
	}
	lockIntent := strings.TrimSpace(query.LockIntent)
	if lockIntent == "" {
		lockIntent = recordmodel.RecordQueryLockNone
	}
	actionTx := actionExecutionTransaction(ctx)
	switch lockIntent {
	case recordmodel.RecordQueryLockNone:
	case recordmodel.RecordQueryLockForUpdate, recordmodel.RecordQueryLockForUpdateSkipLocked:
		if actionTx == nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("record query lock intent %q requires a transactional claim owner", lockIntent)
		}
	default:
		return recordmodel.RecordPageResult{}, fmt.Errorf("record query lock intent %q is invalid", lockIntent)
	}
	s := r.store
	if query.ScopeDiagnostic != nil {
		return recordmodel.RecordPageResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: query.ScopeDiagnostic.Code, Params: map[string]string{"object_key": query.ScopeDiagnostic.ObjectKey, "detail": query.ScopeDiagnostic.Detail}}
	}
	query = recordQueryDBValues(s.RuntimeEngine, object, query)
	executor := r.queryExecutor(ctx)
	var readTx *sql.Tx
	if query.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*query.ScopeExpression) {
		if actionTx == nil {
			readTx, err = r.database().BeginTx(ctx, recordScopeReadTxOptions(s.RuntimeEngine))
			if err != nil {
				return recordmodel.RecordPageResult{}, fmt.Errorf("begin record scope snapshot: %w", err)
			}
			defer readTx.Rollback()
			executor = readTx
		}
		resolved, resolveErr := querypersistence.ResolveScopeMembership(s, workspaceID, *query.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, args ...any) ([]string, error) {
			rows, queryErr := executor.QueryContext(ctx, statement, args...)
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
			return recordmodel.RecordPageResult{}, resolveErr
		}
		query.ScopeExpression = &resolved
	}
	predicate, err := recordLocalizedSearchPredicate(s, workspaceID, object, query)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	countPredicate := predicate
	if afterID := strings.TrimSpace(query.AfterID); afterID != "" {
		if !recordQueryUsesAscendingIDOrder(query.Sort) {
			return recordmodel.RecordPageResult{}, fmt.Errorf("record keyset cursor requires ascending id sort")
		}
		predicate = ormbuilder.And(predicate, ormbuilder.GreaterThan("id", afterID))
	}
	if query.Page > 1 && strings.TrimSpace(query.AfterID) == "" {
		return recordmodel.RecordPageResult{}, fmt.Errorf("record deep pagination requires an id cursor")
	}
	var total int
	if !query.SkipTotal {
		countSQL, countArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, object.Key, workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(countPredicate).Build()
		if buildErr != nil {
			return recordmodel.RecordPageResult{}, buildErr
		}
		if err := executor.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("count records: %w", err)
		}
	}
	fetchLimit := query.PageSize
	if query.SkipTotal || strings.TrimSpace(query.AfterID) != "" {
		fetchLimit++
	}
	selectBuilder := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, object.Key, workspaceID).
		Projections(recordListProjections(query.SelectFields)...).
		Where(predicate).
		OrderBy(recordLocalizedOrders(workspaceID, object, query)...).
		Limit(fetchLimit)
	if lockIntent != recordmodel.RecordQueryLockNone {
		selectBuilder, err = s.RuntimeEngine.ApplyClaimLock(selectBuilder, lockIntent == recordmodel.RecordQueryLockForUpdateSkipLocked)
		if err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("apply record query lock: %w", err)
		}
	}
	listSQL, listArgs, buildErr := selectBuilder.Build()
	if buildErr != nil {
		return recordmodel.RecordPageResult{}, buildErr
	}
	rows, err := executor.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("list records: %w", err)
	}
	records, err := recordsFromRows(s.RuntimeEngine, object, rows)
	if err != nil {
		_ = rows.Close()
		return recordmodel.RecordPageResult{}, err
	}
	_ = rows.Close()
	hasNext := len(records) < total
	if query.SkipTotal || strings.TrimSpace(query.AfterID) != "" {
		hasNext = len(records) > query.PageSize
		if hasNext {
			records = records[:query.PageSize]
		}
	}
	records, err = r.applyRecordLocalization(
		ctx,
		workspaceID,
		object,
		records,
		query.SelectFields,
		query.Locale,
		query.FallbackLocale,
	)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	if readTx != nil {
		if err := readTx.Commit(); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("commit record scope snapshot: %w", err)
		}
	}
	return recordmodel.RecordPageResult{Items: records, Page: query.Page, PageSize: query.PageSize, Total: total, HasNext: hasNext}, nil
}

func recordQueryUsesAscendingIDOrder(sortRules []recordmodel.RecordSortRule) bool {
	if len(sortRules) == 0 {
		return true
	}
	return len(sortRules) == 1 && strings.TrimSpace(sortRules[0].Field) == "id" && !strings.EqualFold(strings.TrimSpace(sortRules[0].Direction), "desc")
}
