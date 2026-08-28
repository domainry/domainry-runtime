package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
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

type reportQueryDialect struct {
	store  *database.RuntimeStore
	offset int
}

func (s reportQueryDialect) Identifier(value string) string { return s.store.Identifier(value) }
func (s reportQueryDialect) TableIdentifier(value string) string {
	return s.store.TableIdentifier(value)
}
func (s reportQueryDialect) Placeholder(index int) string {
	return s.store.Placeholder(s.offset + index)
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
	cteParts := make([]string, 0, len(aliases))
	args := []any{}
	queries := make(map[string]recordmodel.RecordListQuery, len(request.Queries))
	for index, alias := range aliases {
		object := request.Objects[alias]
		query := request.Queries[alias]
		if query.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*query.ScopeExpression) {
			resolved, resolveErr := querypersistence.ResolveScopeMembership(s.store, request.WorkspaceID, *query.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, lookupArgs ...any) ([]string, error) {
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
			query.ScopeExpression = &resolved
		}
		query = recordpersistence.RecordQueryDatabaseValues(s.store.Driver(), object, query)
		whereSQL, whereArgs, whereErr := querypersistence.BuildTenantWhere(reportQueryDialect{store: s.store, offset: len(args)}, request.WorkspaceID, query)
		if whereErr != nil {
			return nil, fmt.Errorf("build report source %s scope: %w", alias, whereErr)
		}
		args = append(args, whereArgs...)
		columns := reportStoreSourceColumns(query.SelectFields)
		cteParts = append(cteParts, fmt.Sprintf("%s AS (SELECT %s FROM %s%s)", s.store.Identifier(reportStoreCTE(index)), reportStoreColumnList(s.store, columns), s.store.TableIdentifier(object.Key), whereSQL))
		queries[alias] = query
	}

	selected := reportStoreSelectedColumns(aliases, request.Objects, queries)
	selectSQL := make([]string, 0, len(selected))
	for _, column := range selected {
		selectSQL = append(selectSQL, reportStoreReference(s.store, column.alias, column.field.Key))
	}
	fromSQL := s.store.Identifier(reportStoreCTE(0)) + " " + s.store.Identifier(aliases[0])
	for joinIndex, join := range request.Plan.Dataset.Joins {
		joinKeyword := " JOIN "
		if strings.TrimSpace(join.Type) == "left" {
			joinKeyword = " LEFT JOIN "
		}
		alias := strings.TrimSpace(join.Alias)
		equalities := make([]string, 0, len(join.Equalities()))
		for _, equality := range join.Equalities() {
			equalities = append(equalities, reportStoreReference(s.store, join.LeftAlias, equality.LeftField)+" = "+reportStoreReference(s.store, alias, equality.RightField))
		}
		fromSQL += joinKeyword + s.store.Identifier(reportStoreCTE(joinIndex+1)) + " " + s.store.Identifier(alias) + " ON " + strings.Join(equalities, " AND ")
	}
	globalWhere, globalArgs, err := reportStoreGlobalFilter(s.store, request.Plan.Dataset, request.Objects, len(args))
	if err != nil {
		return nil, err
	}
	args = append(args, globalArgs...)
	statement := "WITH " + strings.Join(cteParts, ", ") + " SELECT " + strings.Join(selectSQL, ", ") + " FROM " + fromSQL + globalWhere
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
		// Destinations are *any and the SELECT list is constructed from the same
		// selected slice, so database/sql has neither a conversion nor arity
		// failure mode here. Driver iteration failures surface through Rows.Err.
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
				record.Data[column.field.Key] = recordpersistence.NormalizeRecordDatabaseValue(s.store.Driver(), column.field, raw[index])
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
		query := request.Queries[alias]
		if query.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*query.ScopeExpression) {
			resolved, resolveErr := querypersistence.ResolveScopeMembership(s.store, request.WorkspaceID, *query.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, lookupArgs ...any) ([]string, error) {
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
			query.ScopeExpression = &resolved
		}
		query = recordpersistence.RecordQueryDatabaseValues(s.store.Driver(), object, query)
		whereSQL, args, whereErr := querypersistence.BuildTenantWhere(reportQueryDialect{store: s.store}, request.WorkspaceID, query)
		if whereErr != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, whereErr
		}
		statement := "SELECT COUNT(*), COALESCE(MAX(" + s.store.Identifier("updated_at") + "), '') FROM " + s.store.TableIdentifier(object.Key) + whereSQL
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

func reportStoreColumnList(store *database.RuntimeStore, columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = store.Identifier(column)
	}
	return strings.Join(quoted, ", ")
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

func reportStoreReference(store *database.RuntimeStore, alias, field string) string {
	return store.Identifier(strings.TrimSpace(alias)) + "." + store.Identifier(strings.TrimSpace(field))
}

func reportStoreGlobalFilter(store *database.RuntimeStore, dataset reportmodel.ReportDatasetSchema, objects map[string]definitionmodel.ObjectSchema, offset int) (string, []any, error) {
	clauses := []string{}
	args := []any{}
	for _, filter := range dataset.Filters {
		operator := strings.TrimSpace(filter.Operator)
		if operator == "contains" || operator == "starts_with" || operator == "ends_with" {
			continue
		}
		field := reportStoreField(objects[strings.TrimSpace(filter.Field.SourceAlias)], filter.Field.FieldKey)
		reference := reportStoreReference(store, filter.Field.SourceAlias, filter.Field.FieldKey)
		placeholder := func(value any) string {
			args = append(args, recordpersistence.RecordDatabaseFieldValue(store.Driver(), field, value))
			return store.Placeholder(offset + len(args))
		}
		switch operator {
		case "eq", "ne", "gt", "gte", "lt", "lte":
			comparison := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[operator]
			clauses = append(clauses, reference+" "+comparison+" "+placeholder(filter.Value))
		case "in", "not_in":
			values := make([]string, 0, len(filter.Values))
			for _, value := range filter.Values {
				values = append(values, placeholder(value))
			}
			if len(values) == 0 {
				return "", nil, fmt.Errorf("report filter %s requires values", operator)
			}
			keyword := "IN"
			if operator == "not_in" {
				keyword = "NOT IN"
			}
			clauses = append(clauses, reference+" "+keyword+" ("+strings.Join(values, ", ")+")")
		case "between":
			if len(filter.Values) != 2 {
				return "", nil, fmt.Errorf("report filter between requires two values")
			}
			clauses = append(clauses, "("+reference+" >= "+placeholder(filter.Values[0])+" AND "+reference+" <= "+placeholder(filter.Values[1])+")")
		case "is_null":
			clauses = append(clauses, reference+" IS NULL")
		case "not_null":
			clauses = append(clauses, reference+" IS NOT NULL")
		default:
			return "", nil, fmt.Errorf("unsupported report filter operator %q", operator)
		}
	}
	if len(clauses) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
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
