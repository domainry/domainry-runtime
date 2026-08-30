package record

import (
	"context"
	"fmt"
	"sort"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Columns("record_id", "field_key", "locale", "text_value").Where(ormbuilder.And(
		ormbuilder.Equal("object_key", strings.TrimSpace(object.Key)),
		ormbuilder.In("record_id", recordLocalizationAny(recordIDs)...),
		ormbuilder.In("field_key", recordLocalizationAny(fieldKeys)...),
		ormbuilder.In("locale", recordLocalizationAny(locales)...),
	)).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.queryExecutor(ctx).QueryContext(ctx, query, args...)
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
		predicate := ormbuilder.And(ormbuilder.Equal("object_key", commitObject.Key), ormbuilder.Equal("record_id", recordID), ormbuilder.Equal("field_key", value.FieldKey), ormbuilder.Equal("locale", value.Locale))
		deleteSQL, deleteArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Where(predicate).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, deleteSQL, deleteArgs...); err != nil {
			return fmt.Errorf("replace record localized value: %w", err)
		}
		if strings.TrimSpace(value.TextValue) == "" {
			continue
		}
		insert, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Columns("object_key", "record_id", "field_key", "locale", "text_value", "created_at", "updated_at").Values(commitObject.Key, recordID, value.FieldKey, value.Locale, value.TextValue, changedAt, changedAt).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			return fmt.Errorf("insert record localized value: %w", err)
		}
	}
	return nil
}

func (r RecordStore) deleteRecordLocalizedValuesTx(ctx context.Context, tx TransactionExecutor, workspaceID, objectKey, recordID string) error {
	query, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, recordLocalizedValueTable, workspaceID).Where(ormbuilder.And(ormbuilder.Equal("object_key", objectKey), ormbuilder.Equal("record_id", recordID))).Build()
	if buildErr != nil {
		return buildErr
	}
	_, err := tx.ExecContext(ctx, query, args...)
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

func recordLocalizedSearchPredicate(s *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (ormbuilder.Predicate, error) {
	localized := localizedSearchFields(object, query.SearchFields)
	if strings.TrimSpace(query.Locale) == "" || strings.TrimSpace(query.Search) == "" || len(localized) == 0 {
		return s.TenantListPredicate(workspaceID, query)
	}
	baseQuery := query
	baseQuery.Search = ""
	baseQuery.SearchFields = nil
	basePredicate, err := s.TenantListPredicate(workspaceID, baseQuery)
	if err != nil {
		return nil, err
	}
	searchValue := "%" + strings.ToLower(strings.TrimSpace(query.Search)) + "%"
	searchPredicates := make([]ormbuilder.Predicate, 0, len(query.SearchFields)+1)
	for _, field := range query.SearchFields {
		searchPredicates = append(searchPredicates, ormbuilder.LikeValue(ormbuilder.Lower(ormbuilder.Column(field)), searchValue))
	}
	const alias = "record_i18n_search"
	localizedValues := recordLocalizationAny(localized)
	locales := recordLocalizationAny(recordmodel.RecordLocalizationLocales(query.Locale, query.FallbackLocale))
	conditions := []ormbuilder.Predicate{
		ormbuilder.EqualValue(ormbuilder.QualifiedColumn(alias, "workspace_id"), workspaceID),
		ormbuilder.EqualValue(ormbuilder.QualifiedColumn(alias, "object_key"), object.Key),
		ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn(alias, "record_id"), ormbuilder.TableColumn(object.Key, "id")),
		ormbuilder.InExpression(ormbuilder.QualifiedColumn(alias, "field_key"), localizedValues...),
		ormbuilder.InExpression(ormbuilder.QualifiedColumn(alias, "locale"), locales...),
		ormbuilder.LikeValue(ormbuilder.Lower(ormbuilder.QualifiedColumn(alias, "text_value")), searchValue),
	}
	subquery := ormbuilder.NewSelectBuilder(s.SQLRenderer, recordLocalizedValueTable).Alias(alias).Projections(ormbuilder.Project(ormbuilder.Value(1))).Where(ormbuilder.And(conditions...))
	searchPredicates = append(searchPredicates, ormbuilder.ExistsSubquery(subquery))
	return ormbuilder.And(basePredicate, ormbuilder.Or(searchPredicates...)), nil
}

func recordLocalizedOrders(workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) []ormbuilder.Order {
	localizedSet := map[string]bool{}
	for _, field := range recordmodel.RecordLocalizedFieldKeys(object) {
		localizedSet[field] = true
	}
	orders := make([]ormbuilder.Order, 0, len(query.Sort)+1)
	for _, rule := range query.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if strings.TrimSpace(query.Locale) == "" || !localizedSet[rule.Field] {
			if direction == "DESC" {
				orders = append(orders, ormbuilder.Descending(rule.Field))
			} else {
				orders = append(orders, ormbuilder.Ascending(rule.Field))
			}
			continue
		}
		values := []ormbuilder.Expression{}
		for _, locale := range recordmodel.RecordLocalizationLocales(query.Locale, query.FallbackLocale) {
			values = append(values, ormbuilder.ScalarSubquery(recordLocalizedValueTable, ormbuilder.Column("text_value"), ormbuilder.And(
				ormbuilder.Equal("workspace_id", workspaceID), ormbuilder.Equal("object_key", object.Key),
				ormbuilder.EqualExpressions(ormbuilder.Column("record_id"), ormbuilder.TableColumn(object.Key, "id")),
				ormbuilder.Equal("field_key", rule.Field), ormbuilder.Equal("locale", locale),
			)))
		}
		values = append(values, ormbuilder.Column(rule.Field))
		expression := ormbuilder.Coalesce(values...)
		if direction == "DESC" {
			orders = append(orders, ormbuilder.DescendingExpression(expression))
		} else {
			orders = append(orders, ormbuilder.AscendingExpression(expression))
		}
	}
	if len(orders) == 0 {
		orders = append(orders, ormbuilder.Ascending("id"))
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
