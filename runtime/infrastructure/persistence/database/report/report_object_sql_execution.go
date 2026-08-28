package report

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

var _ reportcontract.ReportObjectSQLExecutor = (*ReportDatasetStore)(nil)

func (s *ReportDatasetStore) ExecuteReportObjectSQL(ctx context.Context, request reportcontract.ReportObjectSQLExecutionRequest) (reportcontract.ReportObjectSQLExecutionResult, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("report object SQL store is unavailable")
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	queryContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := s.beginTx(queryContext, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("begin report object SQL snapshot: %w", err)
	}
	defer tx.Rollback()
	cteParts, sourceArgs, err := s.reportObjectSQLSources(queryContext, tx, request)
	if err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, err
	}
	if request.PageOffset < 0 || request.PageSize < 0 {
		return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("invalid report object SQL page")
	}
	result := reportcontract.ReportObjectSQLExecutionResult{Rows: []map[string]string{}}
	if request.PageSize > 0 {
		countArgs := append([]any(nil), sourceArgs...)
		countEmitter := reportObjectSQLEmitter{dialect: s.store, parameters: request.Parameters, args: &countArgs}
		completeStatement, countErr := countEmitter.statement(request.Plan, cteParts)
		if countErr != nil {
			return reportcontract.ReportObjectSQLExecutionResult{}, countErr
		}
		countStatement := "SELECT COUNT(*) FROM (" + completeStatement + ") AS " + s.store.Identifier("domainry_report_count")
		if countErr = tx.QueryRowContext(queryContext, countStatement, countArgs...).Scan(&result.Total); countErr != nil {
			return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("count report object SQL: %w", countErr)
		}
		result.TotalKnown = true
		if request.PageOffset >= request.Plan.Limit {
			if err := tx.Commit(); err != nil {
				return reportcontract.ReportObjectSQLExecutionResult{}, err
			}
			return result, nil
		}
	}
	args := append([]any(nil), sourceArgs...)
	emitter := reportObjectSQLEmitter{dialect: s.store, parameters: request.Parameters, args: &args}
	statement, err := emitter.statementPage(request.Plan, cteParts, request.PageOffset, request.PageSize)
	if err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, err
	}
	rows, err := tx.QueryContext(queryContext, statement, args...)
	if err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("query report object SQL: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		raw := make([]any, len(request.Plan.ResultSchema))
		destinations := make([]any, len(raw))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return reportcontract.ReportObjectSQLExecutionResult{}, fmt.Errorf("scan report object SQL: %w", err)
		}
		row := map[string]string{}
		for index, column := range request.Plan.ResultSchema {
			if raw[index] == nil {
				continue
			}
			normalized, normalizeErr := reportObjectSQLResultValue(s.store.Driver(), column, raw[index])
			if normalizeErr != nil {
				return reportcontract.ReportObjectSQLExecutionResult{}, normalizeErr
			}
			row[column.Key] = normalized
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, err
	}
	if request.PageSize > 0 && len(result.Rows) > request.PageSize {
		result.Rows = result.Rows[:request.PageSize]
		result.HasMore = true
	}
	if err := tx.Commit(); err != nil {
		return reportcontract.ReportObjectSQLExecutionResult{}, err
	}
	return result, nil
}

func (s *ReportDatasetStore) reportObjectSQLSources(ctx context.Context, tx *sql.Tx, request reportcontract.ReportObjectSQLExecutionRequest) ([]string, []any, error) {
	cteParts, args := make([]string, 0, len(request.Plan.Sources)), []any{}
	for index, source := range request.Plan.Sources {
		object, query := request.Objects[source.Alias], request.Queries[source.Alias]
		if query.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*query.ScopeExpression) {
			resolved, err := querypersistence.ResolveScopeMembership(s.store, request.WorkspaceID, *query.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, lookupArgs ...any) ([]string, error) {
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
			if err != nil {
				return nil, nil, err
			}
			query.ScopeExpression = &resolved
		}
		query = recordpersistence.RecordQueryDatabaseValues(s.store.Driver(), object, query)
		whereSQL, whereArgs, err := querypersistence.BuildTenantWhere(reportQueryDialect{store: s.store, offset: len(args)}, request.WorkspaceID, query)
		if err != nil {
			return nil, nil, fmt.Errorf("build report object SQL source %s scope: %w", source.Alias, err)
		}
		args = append(args, whereArgs...)
		columns := reportStoreSourceColumns(query.SelectFields)
		cteParts = append(cteParts, fmt.Sprintf("%s AS (SELECT %s FROM %s%s)", s.store.Identifier(reportObjectSQLCTE(index)), reportStoreColumnList(s.store, columns), s.store.TableIdentifier(object.Key), whereSQL))
	}
	return cteParts, args, nil
}

func reportObjectSQLCTE(index int) string { return fmt.Sprintf("report_object_sql_source_%d", index) }

func reportObjectSQLResultValue(driver string, column reportmodel.ReportResultColumnSchema, raw any) (string, error) {
	if bytes, ok := raw.([]byte); ok {
		raw = string(bytes)
	}
	if column.Type == "currency" {
		if driver == "sqlite" {
			minor, ok := new(big.Int).SetString(strings.TrimSpace(fmt.Sprint(raw)), 10)
			if !ok {
				return "", fmt.Errorf("invalid exact currency result for %s", column.Key)
			}
			return decimal.NewFromBigInt(minor, int32(-column.Scale)).StringFixed(int32(column.Scale)), nil
		}
		value, err := decimal.NewFromString(strings.TrimSpace(fmt.Sprint(raw)))
		if err != nil {
			return "", err
		}
		return value.StringFixed(int32(column.Scale)), nil
	}
	if column.Type == "decimal" {
		if driver == "sqlite" && column.Precision > 0 {
			minor, ok := new(big.Int).SetString(strings.TrimSpace(fmt.Sprint(raw)), 10)
			if !ok {
				return "", fmt.Errorf("invalid exact decimal result for %s", column.Key)
			}
			return decimal.NewFromBigInt(minor, int32(-column.Scale)).StringFixed(int32(column.Scale)), nil
		}
		switch raw.(type) {
		case float32, float64:
			return "", fmt.Errorf("backend.report.object_sql_decimal_float_unsafe")
		}
		value, err := decimal.NewFromString(strings.TrimSpace(fmt.Sprint(raw)))
		if err != nil {
			return "", err
		}
		return value.String(), nil
	}
	if column.Type == "number" {
		switch value := raw.(type) {
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return "", fmt.Errorf("backend.report.object_sql_number_non_finite")
			}
			return strconv.FormatFloat(value, 'g', -1, 64), nil
		case float32:
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return "", fmt.Errorf("backend.report.object_sql_number_non_finite")
			}
			return strconv.FormatFloat(float64(value), 'g', -1, 32), nil
		}
	}
	return fmt.Sprint(raw), nil
}
