package record

import (
	"github.com/domainry/domainry-orm/query"
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

func (r RecordStore) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, queryValue recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	queryValue.FilterExpression, err = recordvalidation.RecordNormalizeFilterExpression(object, queryValue.FilterExpression)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record filter: %w", err)
	}
	queryValue.SelectFields, err = recordvalidation.RecordNormalizeSelectFields(object, queryValue.SelectFields)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record projection: %w", err)
	}
	lockIntent := strings.TrimSpace(queryValue.LockIntent)
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
	if queryValue.AuthorizationDiagnostic != nil {
		return recordmodel.RecordPageResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: queryValue.AuthorizationDiagnostic.Code, Params: map[string]string{"object_key": queryValue.AuthorizationDiagnostic.ObjectKey, "detail": queryValue.AuthorizationDiagnostic.Detail}}
	}
	queryValue = recordQueryDBValues(s.RuntimeEngine, object, queryValue)
	executor := r.queryExecutor(ctx)
	var readTx *sql.Tx
	if queryValue.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*queryValue.ScopeExpression) {
		if actionTx == nil {
			readTx, err = r.database().BeginTx(ctx, recordScopeReadTxOptions(s.RuntimeEngine))
			if err != nil {
				return recordmodel.RecordPageResult{}, fmt.Errorf("begin record scope snapshot: %w", err)
			}
			defer readTx.Rollback()
			executor = readTx
		}
		resolved, resolveErr := querypersistence.ResolveScopeMembership(s, workspaceID, *queryValue.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, args ...any) ([]string, error) {
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
		queryValue.ScopeExpression = &resolved
	}
	predicate, err := recordLocalizedSearchPredicate(s, workspaceID, object, queryValue)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	countPredicate := predicate
	if afterID := strings.TrimSpace(queryValue.AfterID); afterID != "" {
		if !recordQueryUsesAscendingIDOrder(queryValue.Sort) {
			return recordmodel.RecordPageResult{}, fmt.Errorf("record keyset cursor requires ascending id sort")
		}
		predicate = query.And(predicate, query.GreaterThan("id", afterID))
	}
	if queryValue.Page > 1 && strings.TrimSpace(queryValue.AfterID) == "" {
		return recordmodel.RecordPageResult{}, fmt.Errorf("record deep pagination requires an id cursor")
	}
	var total int
	if !queryValue.SkipTotal {
		countSQL, countArgs, buildErr := query.NewWorkspaceSelectBuilder(s.SQLRenderer, object.Key, workspaceID).Projections(query.Project(query.CountAll())).Where(countPredicate).Build()
		if buildErr != nil {
			return recordmodel.RecordPageResult{}, buildErr
		}
		if err := executor.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("count records: %w", err)
		}
	}
	fetchLimit := queryValue.PageSize
	if queryValue.SkipTotal || strings.TrimSpace(queryValue.AfterID) != "" {
		fetchLimit++
	}
	selectBuilder := query.NewWorkspaceSelectBuilder(s.SQLRenderer, object.Key, workspaceID).
		Projections(recordListProjections(queryValue.SelectFields)...).
		Where(predicate).
		OrderBy(recordLocalizedOrders(workspaceID, object, queryValue)...).
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
	var sortFields []string
	if queryValue.StableNullsLast {
		for _, rule := range queryValue.Sort {
			sortFields = append(sortFields, rule.Field)
		}
	}
	records, err := recordsFromRows(s.RuntimeEngine, object, rows, sortFields...)
	if err != nil {
		_ = rows.Close()
		return recordmodel.RecordPageResult{}, err
	}
	_ = rows.Close()
	hasNext := len(records) < total
	if queryValue.SkipTotal || strings.TrimSpace(queryValue.AfterID) != "" {
		hasNext = len(records) > queryValue.PageSize
		if hasNext {
			records = records[:queryValue.PageSize]
		}
	}
	records, err = r.applyRecordLocalization(
		ctx,
		workspaceID,
		object,
		records,
		queryValue.SelectFields,
		queryValue.Locale,
		queryValue.FallbackLocale,
	)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	if readTx != nil {
		if err := readTx.Commit(); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("commit record scope snapshot: %w", err)
		}
	}
	return recordmodel.RecordPageResult{Items: records, Page: queryValue.Page, PageSize: queryValue.PageSize, Total: total, HasNext: hasNext}, nil
}

func recordQueryUsesAscendingIDOrder(sortRules []recordmodel.RecordSortRule) bool {
	if len(sortRules) == 0 {
		return true
	}
	return len(sortRules) == 1 && strings.TrimSpace(sortRules[0].Field) == "id" && !strings.EqualFold(strings.TrimSpace(sortRules[0].Direction), "desc")
}
