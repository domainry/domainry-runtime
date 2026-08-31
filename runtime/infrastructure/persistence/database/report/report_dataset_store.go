package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

var _ reportcontract.ReportDatasetRowReader = (*ReportDatasetStore)(nil)
var _ reportcontract.ReportSnapshotSourceVersionReader = (*ReportDatasetStore)(nil)

type ReportDatasetStore struct {
	store   *database.RuntimeStore
	beginTx func(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func NewReportDatasetStore(store *database.RuntimeStore) *ReportDatasetStore {
	result := &ReportDatasetStore{store: store}
	if store != nil && store.DB() != nil {
		result.beginTx = store.DB().BeginTx
	}
	return result
}

type reportSelectedColumn struct {
	alias string
	field definitionmodel.FieldSchema
}

func (s *ReportDatasetStore) ReadReportDatasetRows(ctx context.Context, request reportcontract.ReportDatasetRowReadRequest) ([]reportcontract.ReportDatasetRecordRow, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return nil, fmt.Errorf("report dataset store is unavailable")
	}
	tx, err := s.beginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin report dataset snapshot: %w", err)
	}
	defer tx.Rollback()

	aliases := reportStoreAliasOrder(request.Plan.Dataset)
	cteQueries := make([]*query.SelectBuilder, 0, len(aliases))
	queries := make(map[string]recordmodel.RecordListQuery, len(request.Queries))
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
				return nil, resolveErr
			}
			queryValue.ScopeExpression = &resolved
		}
		queryValue = recordpersistence.RecordQueryDatabaseValues(s.store.RuntimeEngine, object, queryValue)
		predicate, predicateErr := querypersistence.BuildTenantPredicate(s.store, request.WorkspaceID, queryValue)
		if predicateErr != nil {
			return nil, fmt.Errorf("build report source %s scope: %w", alias, predicateErr)
		}
		columns := reportStoreSourceColumns(queryValue.SelectFields)
		cteQueries = append(cteQueries, query.NewSelectBuilder(s.store.SQLRenderer, object.Key).Columns(columns...).Where(predicate))
		queries[alias] = queryValue
	}

	selected := reportStoreSelectedColumns(aliases, request.Objects, queries)
	projections := make([]query.Projection, 0, len(selected))
	for _, column := range selected {
		projections = append(projections, query.Project(query.QualifiedColumn(column.alias, column.field.Key)))
	}
	selectBuilder := query.NewSelectFromCTE(s.store.SQLRenderer, reportStoreCTE(0), aliases[0]).Projections(projections...)
	joins := make([]query.Join, 0, len(request.Plan.Dataset.Joins))
	for joinIndex, join := range request.Plan.Dataset.Joins {
		alias := strings.TrimSpace(join.Alias)
		equalities := make([]query.Predicate, 0, len(join.Equalities()))
		for _, equality := range join.Equalities() {
			equalities = append(equalities, query.EqualExpressions(query.QualifiedColumn(join.LeftAlias, equality.LeftField), query.QualifiedColumn(alias, equality.RightField)))
		}
		if strings.TrimSpace(join.Type) == "left" {
			joins = append(joins, query.LeftJoinCTE(reportStoreCTE(joinIndex+1), alias, query.And(equalities...)))
		} else {
			joins = append(joins, query.InnerJoinCTE(reportStoreCTE(joinIndex+1), alias, query.And(equalities...)))
		}
	}
	selectBuilder.Join(joins...)
	globalPredicate, err := reportStoreGlobalPredicate(s.store, request.Plan.Dataset, request.Objects)
	if err != nil {
		return nil, err
	}
	if globalPredicate != nil {
		selectBuilder.Where(globalPredicate)
	}
	for index, cteQuery := range cteQueries {
		selectBuilder.With(reportStoreCTE(index), cteQuery)
	}
	statement, args, err := selectBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("build report dataset query: %w", err)
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query report dataset rows: %w", err)
	}
	defer rows.Close()

	result := []reportcontract.ReportDatasetRecordRow{}
	for rows.Next() {
		raw := make([]any, len(selected))
		destinations := make([]any, len(selected))
		for index := range raw {
			destinations[index] = &raw[index]
		}

		_ = rows.Scan(destinations...)
		records := map[string]recordmodel.Record{}
		present := map[string]bool{}
		for index, column := range selected {
			if raw[index] == nil {
				continue
			}
			record := records[column.alias]
			switch column.field.Key {
			case "id":
				record.ID = fmt.Sprint(raw[index])
				present[column.alias] = record.ID != ""
			case "created_at":
				record.CreatedAt = fmt.Sprint(raw[index])
			case "updated_at":
				record.UpdatedAt = fmt.Sprint(raw[index])
			default:
				if record.Data == nil {
					record.Data = map[string]any{}
				}
				record.Data[column.field.Key] = recordpersistence.NormalizeRecordDatabaseValue(s.store.RuntimeEngine, column.field, raw[index])
			}
			records[column.alias] = record
		}
		for alias := range records {
			if !present[alias] {
				delete(records, alias)
			}
		}
		result = append(result, reportcontract.ReportDatasetRecordRow{Records: records})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ReportDatasetStore) ReadReportSnapshotSourceVersion(ctx context.Context, request reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return reportmodel.ReportSnapshotSourceVersion{}, fmt.Errorf("report dataset store is unavailable")
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

func reportStoreAliasOrder(dataset reportmodel.ReportDatasetSchema) []string {
	aliases := []string{strings.TrimSpace(dataset.Source.Alias)}
	for _, join := range dataset.Joins {
		aliases = append(aliases, strings.TrimSpace(join.Alias))
	}
	return aliases
}

func reportStoreCTE(index int) string { return fmt.Sprintf("report_source_%d", index) }

func reportStoreSourceColumns(selected []string) []string {
	columns := []string{"id", "created_at", "updated_at"}
	seen := map[string]bool{"id": true, "created_at": true, "updated_at": true}
	for _, field := range selected {
		field = strings.TrimSpace(field)
		if field != "" && !seen[field] {
			seen[field] = true
			columns = append(columns, field)
		}
	}
	return columns
}

func reportStoreSelectedColumns(aliases []string, objects map[string]definitionmodel.ObjectSchema, queries map[string]recordmodel.RecordListQuery) []reportSelectedColumn {
	selected := []reportSelectedColumn{}
	for _, alias := range aliases {
		fields := map[string]definitionmodel.FieldSchema{
			"id": {Key: "id", Type: "text"}, "created_at": {Key: "created_at", Type: "datetime"}, "updated_at": {Key: "updated_at", Type: "datetime"},
		}
		for _, field := range objects[alias].Fields {
			fields[field.Key] = field
		}
		for _, key := range reportStoreSourceColumns(queries[alias].SelectFields) {
			selected = append(selected, reportSelectedColumn{alias: alias, field: fields[key]})
		}
	}
	return selected
}

func reportStoreGlobalPredicate(store *database.RuntimeStore, dataset reportmodel.ReportDatasetSchema, objects map[string]definitionmodel.ObjectSchema) (query.Predicate, error) {
	predicates := []query.Predicate{}
	for _, filter := range dataset.Filters {
		operator := strings.TrimSpace(filter.Operator)
		if operator == "contains" || operator == "starts_with" || operator == "ends_with" {
			continue
		}
		field := reportStoreField(objects[strings.TrimSpace(filter.Field.SourceAlias)], filter.Field.FieldKey)
		reference := query.QualifiedColumn(strings.TrimSpace(filter.Field.SourceAlias), strings.TrimSpace(filter.Field.FieldKey))
		value := func(raw any) any { return recordpersistence.RecordDatabaseFieldValue(store.RuntimeEngine, field, raw) }
		switch operator {
		case "eq":
			predicates = append(predicates, query.EqualValue(reference, value(filter.Value)))
		case "ne":
			predicates = append(predicates, query.NotEqualValue(reference, value(filter.Value)))
		case "gt":
			predicates = append(predicates, query.GreaterThanExpression(reference, value(filter.Value)))
		case "gte":
			predicates = append(predicates, query.GreaterThanOrEqualValue(reference, value(filter.Value)))
		case "lt":
			predicates = append(predicates, query.LessThanValue(reference, value(filter.Value)))
		case "lte":
			predicates = append(predicates, query.LessThanOrEqualValue(reference, value(filter.Value)))
		case "in", "not_in":
			values := make([]any, 0, len(filter.Values))
			for _, raw := range filter.Values {
				values = append(values, value(raw))
			}
			if len(values) == 0 {
				return nil, fmt.Errorf("report filter %s requires values", operator)
			}
			if operator == "not_in" {
				predicates = append(predicates, query.NotInExpression(reference, values...))
			} else {
				predicates = append(predicates, query.InExpression(reference, values...))
			}
		case "between":
			if len(filter.Values) != 2 {
				return nil, fmt.Errorf("report filter between requires two values")
			}
			predicates = append(predicates, query.And(query.GreaterThanOrEqualValue(reference, value(filter.Values[0])), query.LessThanOrEqualValue(reference, value(filter.Values[1]))))
		case "is_null":
			predicates = append(predicates, query.IsNullExpression(reference))
		case "not_null":
			predicates = append(predicates, query.IsNotNullExpression(reference))
		default:
			return nil, fmt.Errorf("unsupported report filter operator %q", operator)
		}
	}
	if len(predicates) == 0 {
		return nil, nil
	}
	return query.And(predicates...), nil
}

func reportStoreField(object definitionmodel.ObjectSchema, fieldKey string) definitionmodel.FieldSchema {
	fieldKey = strings.TrimSpace(fieldKey)
	if fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at" {
		return definitionmodel.FieldSchema{Key: fieldKey, Type: "text"}
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return field
		}
	}
	return definitionmodel.FieldSchema{Key: fieldKey, Type: "text"}
}

func reportStoreSortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
