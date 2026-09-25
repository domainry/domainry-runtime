package record

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

const recordLocalizedValueTable = "_record_localized_values"

func (r RecordStore) ListRecordLocalizedValues(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordIDs, fieldKeys, locales []string) ([]recordmodel.RecordLocalizedValue, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	recordIDs = nonEmptyRecordLocalizationValues(recordIDs)
	fieldKeys = nonEmptyRecordLocalizationValues(fieldKeys)
	locales = nonEmptyRecordLocalizationValues(locales)
	if len(recordIDs) == 0 || len(fieldKeys) == 0 || len(locales) == 0 {
		return nil, nil
	}
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Columns("record_id", "field_key", "locale", "text_value").Where(query.And(
		query.Equal("object_key", strings.TrimSpace(object.Key)),
		query.In("record_id", recordLocalizationAny(recordIDs)...),
		query.In("field_key", recordLocalizationAny(fieldKeys)...),
		query.In("locale", recordLocalizationAny(locales)...),
	)).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.queryExecutor(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, fmt.Errorf("list record localized values: %w", err)
	}
	defer rows.Close()
	values := []recordmodel.RecordLocalizedValue{}
	for rows.Next() {
		var value recordmodel.RecordLocalizedValue
		if err := rows.Scan(&value.RecordID, &value.FieldKey, &value.Locale, &value.TextValue); err != nil {
			return nil, fmt.Errorf("scan record localized value: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r RecordStore) GetRecordLocalized(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID, locale, secondaryLocale string) (recordmodel.Record, bool, error) {
	record, found, err := r.GetRecord(ctx, workspaceID, object, recordID)
	if err != nil || !found {
		return record, found, err
	}
	records, err := r.applyRecordLocalization(ctx, workspaceID, object, []recordmodel.Record{record}, nil, locale, secondaryLocale)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	return records[0], true, nil
}

func (r RecordStore) applyRecordLocalization(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, records []recordmodel.Record, selectFields []string, locale, secondaryLocale string) ([]recordmodel.Record, error) {
	locale = strings.TrimSpace(locale)
	secondaryLocale = strings.TrimSpace(secondaryLocale)
	if locale == "" || len(records) == 0 {
		return records, nil
	}
	fields := recordLocalizationProjectionFields(object, selectFields)
	if len(fields) == 0 {
		return records, nil
	}
	recordIDs := make([]string, 0, len(records))
	for _, record := range records {
		recordIDs = append(recordIDs, record.ID)
	}
	locales := recordmodel.RecordLocalizationLocales(locale, secondaryLocale)
	values, err := r.ListRecordLocalizedValues(ctx, workspaceID, object, recordIDs, fields, locales)
	if err != nil {
		return nil, err
	}
	lookup := map[string]string{}
	for _, value := range values {
		lookup[recordLocalizedLookupKey(value.RecordID, value.FieldKey, value.Locale)] = value.TextValue
	}
	for index := range records {
		if records[index].Data == nil {
			records[index].Data = map[string]any{}
		}
		resolution := &recordmodel.RecordLocalizationResolution{RequestedLocale: locale, FallbackLocale: secondaryLocale, FieldSources: map[string]string{}}
		for _, field := range fields {
			resolution.FieldSources[field] = "base"
			if text, ok := lookup[recordLocalizedLookupKey(records[index].ID, field, locale)]; ok {
				records[index].Data[field] = text
				resolution.FieldSources[field] = locale
				continue
			}
			if secondaryLocale != "" && secondaryLocale != locale {
				if text, ok := lookup[recordLocalizedLookupKey(records[index].ID, field, secondaryLocale)]; ok {
					records[index].Data[field] = text
					resolution.FieldSources[field] = secondaryLocale
				}
			}
		}
		records[index].Localization = resolution
	}
	return records, nil
}

func (r RecordStore) applyRecordLocalizedMutationsTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commitObject definitionmodel.ObjectSchema, recordID, changedAt string, mutations []recordmodel.RecordLocalizedValueMutation) error {
	if len(mutations) == 0 {
		return nil
	}
	for _, value := range mutations {
		predicate := query.And(query.Equal("object_key", commitObject.Key), query.Equal("record_id", recordID), query.Equal("field_key", value.FieldKey), query.Equal("locale", value.Locale), r.store.SubjectResourceWriteAllowed(workspaceID, commitObject.Key, recordID))
		deleteSQL, deleteArgs, buildErr := query.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Where(predicate).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, deleteSQL, deleteArgs...); err != nil {
			return fmt.Errorf("replace record localized value: %w", err)
		}
		if strings.TrimSpace(value.TextValue) == "" {
			continue
		}
		builder, buildErr := r.store.SubjectEvidenceInsertBuilder(workspaceID, recordLocalizedValueTable,
			[]string{"object_key", "record_id", "field_key", "locale", "text_value", "created_at", "updated_at"},
			[]any{commitObject.Key, recordID, value.FieldKey, value.Locale, value.TextValue, timevalue.Millis(changedAt), timevalue.Millis(changedAt)})
		if buildErr != nil {
			return buildErr
		}
		insert, args, buildErr := builder.Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, insert, args...)
		if err != nil {
			return fmt.Errorf("insert record localized value: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("runtime.subject_erased")
		}
	}
	return nil
}

func (r RecordStore) deleteRecordLocalizedValuesTx(ctx context.Context, tx TransactionExecutor, workspaceID, objectKey, recordID string) error {
	queryValue, args, buildErr := query.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Where(query.And(query.Equal("object_key", objectKey), query.Equal("record_id", recordID))).Build()
	if buildErr != nil {
		return buildErr
	}
	_, err := tx.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return fmt.Errorf("delete record localized values: %w", err)
	}
	return nil
}

func recordLocalizationAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func recordLocalizedSearchPredicate(s *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, queryValue recordmodel.RecordListQuery) (query.Predicate, error) {
	localized := localizedSearchFields(object, queryValue.SearchFields)
	if strings.TrimSpace(queryValue.Locale) == "" || strings.TrimSpace(queryValue.Search) == "" || len(localized) == 0 {
		return s.TenantListPredicate(workspaceID, queryValue)
	}
	baseQuery := queryValue
	baseQuery.Search = ""
	baseQuery.SearchFields = nil
	basePredicate, err := s.TenantListPredicate(workspaceID, baseQuery)
	if err != nil {
		return nil, err
	}
	searchValue := "%" + recordEscapeLikePattern(strings.ToLower(strings.TrimSpace(queryValue.Search))) + "%"
	searchPredicates := make([]query.Predicate, 0, len(queryValue.SearchFields)+1)
	for _, field := range queryValue.SearchFields {
		searchPredicates = append(searchPredicates, query.LikeValueEscaped(query.Lower(query.Column(field)), searchValue))
	}
	const alias = "record_i18n_search"
	localizedValues := recordLocalizationAny(localized)
	locales := recordLocalizationAny(recordmodel.RecordLocalizationLocales(queryValue.Locale, queryValue.FallbackLocale))
	conditions := []query.Predicate{
		query.EqualValue(query.QualifiedColumn(alias, "workspace_id"), workspaceID),
		query.EqualValue(query.QualifiedColumn(alias, "object_key"), object.Key),
		query.EqualExpressions(query.QualifiedColumn(alias, "record_id"), query.TableColumn(object.Key, "id")),
		query.InExpression(query.QualifiedColumn(alias, "field_key"), localizedValues...),
		query.InExpression(query.QualifiedColumn(alias, "locale"), locales...),
		query.LikeValueEscaped(query.Lower(query.QualifiedColumn(alias, "text_value")), searchValue),
	}
	subquery := query.NewSelectBuilder(s.SQLRenderer, recordLocalizedValueTable).Alias(alias).Projections(query.Project(query.Value(1))).Where(query.And(conditions...))
	searchPredicates = append(searchPredicates, query.ExistsSubquery(subquery))
	return query.And(basePredicate, query.Or(searchPredicates...)), nil
}

func recordEscapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, "~", "~~")
	value = strings.ReplaceAll(value, "%", "~%")
	return strings.ReplaceAll(value, "_", "~_")
}

func recordLocalizedOrders(workspaceID string, object definitionmodel.ObjectSchema, queryValue recordmodel.RecordListQuery) []query.Order {
	localizedSet := map[string]bool{}
	for _, field := range recordmodel.RecordLocalizedFieldKeys(object) {
		localizedSet[field] = true
	}
	orders := make([]query.Order, 0, len(queryValue.Sort)+1)
	for _, rule := range queryValue.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if strings.TrimSpace(queryValue.Locale) == "" || !localizedSet[rule.Field] {
			if queryValue.StableNullsLast {
				orders = append(orders, query.AscendingExpression(query.CaseWhen(query.IsNull(rule.Field), 1).Else(0)))
			}
			if direction == "DESC" {
				orders = append(orders, query.Descending(rule.Field))
			} else {
				orders = append(orders, query.Ascending(rule.Field))
			}
			continue
		}
		values := []query.Expression{}
		for _, locale := range recordmodel.RecordLocalizationLocales(queryValue.Locale, queryValue.FallbackLocale) {
			values = append(values, query.ScalarSubquery(recordLocalizedValueTable, query.Column("text_value"), query.And(
				query.Equal("workspace_id", workspaceID), query.Equal("object_key", object.Key),
				query.EqualExpressions(query.Column("record_id"), query.TableColumn(object.Key, "id")),
				query.Equal("field_key", rule.Field), query.Equal("locale", locale),
			)))
		}
		values = append(values, query.Column(rule.Field))
		expression := query.Coalesce(values...)
		if queryValue.StableNullsLast {
			orders = append(orders, query.AscendingExpression(query.CaseWhen(query.IsNullExpression(expression), 1).Else(0)))
		}
		if direction == "DESC" {
			orders = append(orders, query.DescendingExpression(expression))
		} else {
			orders = append(orders, query.AscendingExpression(expression))
		}
	}
	if len(orders) == 0 {
		orders = append(orders, query.Ascending("id"))
	}
	return orders
}

func recordLocalizationProjectionFields(object definitionmodel.ObjectSchema, selectFields []string) []string {
	all := recordmodel.RecordLocalizedFieldKeys(object)
	if len(selectFields) == 0 {
		return all
	}
	selected := map[string]bool{}
	for _, field := range selectFields {
		selected[field] = true
	}
	result := []string{}
	for _, field := range all {
		if selected[field] {
			result = append(result, field)
		}
	}
	return result
}

func localizedSearchFields(object definitionmodel.ObjectSchema, searchFields []string) []string {
	localized := map[string]bool{}
	for _, field := range recordmodel.RecordLocalizedFieldKeys(object) {
		localized[field] = true
	}
	result := []string{}
	for _, field := range searchFields {
		if localized[field] {
			result = append(result, field)
		}
	}
	return result
}

func nonEmptyRecordLocalizationValues(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func recordLocalizedLookupKey(recordID, field, locale string) string {
	return recordID + "\x00" + field + "\x00" + locale
}
