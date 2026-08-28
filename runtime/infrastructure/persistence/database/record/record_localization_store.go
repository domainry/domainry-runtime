package record

import (
	"context"
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const recordLocalizedValueTable = "business_record_localized_value"

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
	s := r.store
	args := []any{workspaceID, strings.TrimSpace(object.Key)}
	query := "SELECT " + stringsJoinIdentifiers(s, "record_id", "field_key", "locale", "text_value") +
		" FROM " + s.TableIdentifier(recordLocalizedValueTable) +
		" WHERE " + s.Identifier("workspace_id") + " = " + s.Placeholder(1) +
		" AND " + s.Identifier("object_key") + " = " + s.Placeholder(2) +
		" AND " + recordLocalizationInClause(s, "record_id", recordIDs, &args) +
		" AND " + recordLocalizationInClause(s, "field_key", fieldKeys, &args) +
		" AND " + recordLocalizationInClause(s, "locale", locales, &args)
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
	s := r.store
	for _, value := range mutations {
		deleteArgs := []any{workspaceID, commitObject.Key, recordID, value.FieldKey, value.Locale}
		deleteSQL := "DELETE FROM " + s.TableIdentifier(recordLocalizedValueTable) + " WHERE " +
			s.Identifier("workspace_id") + " = " + s.Placeholder(1) + " AND " +
			s.Identifier("object_key") + " = " + s.Placeholder(2) + " AND " +
			s.Identifier("record_id") + " = " + s.Placeholder(3) + " AND " +
			s.Identifier("field_key") + " = " + s.Placeholder(4) + " AND " +
			s.Identifier("locale") + " = " + s.Placeholder(5)
		if _, err := tx.ExecContext(ctx, deleteSQL, deleteArgs...); err != nil {
			return fmt.Errorf("replace record localized value: %w", err)
		}
		if strings.TrimSpace(value.TextValue) == "" {
			continue
		}
		columns := []string{"workspace_id", "object_key", "record_id", "field_key", "locale", "text_value", "created_at", "updated_at"}
		args := []any{workspaceID, commitObject.Key, recordID, value.FieldKey, value.Locale, value.TextValue, changedAt, changedAt}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.TableIdentifier(recordLocalizedValueTable)+" ("+stringsJoinIdentifiers(s, columns...)+") VALUES ("+stringsJoinPlaceholders(s, len(columns))+")", args...); err != nil {
			return fmt.Errorf("insert record localized value: %w", err)
		}
	}
	return nil
}

func (r RecordStore) deleteRecordLocalizedValuesTx(ctx context.Context, tx TransactionExecutor, workspaceID, objectKey, recordID string) error {
	s := r.store
	_, err := tx.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier(recordLocalizedValueTable)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("object_key")+" = "+s.Placeholder(2)+" AND "+s.Identifier("record_id")+" = "+s.Placeholder(3), workspaceID, objectKey, recordID)
	if err != nil {
		return fmt.Errorf("delete record localized values: %w", err)
	}
	return nil
}

func recordLocalizedSearchWhere(s *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (string, []any, error) {
	localized := localizedSearchFields(object, query.SearchFields)
	if strings.TrimSpace(query.Locale) == "" || strings.TrimSpace(query.Search) == "" || len(localized) == 0 {
		return s.TenantListWhereClause(workspaceID, query)
	}
	baseQuery := query
	baseQuery.Search = ""
	baseQuery.SearchFields = nil
	whereSQL, args, err := s.TenantListWhereClause(workspaceID, baseQuery)
	if err != nil {
		return "", nil, err
	}
	searchValue := "%" + strings.ToLower(strings.TrimSpace(query.Search)) + "%"
	parts := []string{}
	for _, field := range query.SearchFields {
		args = append(args, searchValue)
		parts = append(parts, "LOWER("+s.Identifier(field)+") LIKE "+s.Placeholder(len(args)))
	}
	alias := s.Identifier("record_i18n_search")
	conditions := []string{
		alias + "." + s.Identifier("workspace_id") + " = " + s.Placeholder(len(args)+1),
		alias + "." + s.Identifier("object_key") + " = " + s.Placeholder(len(args)+2),
		alias + "." + s.Identifier("record_id") + " = " + s.TableIdentifier(object.Key) + "." + s.Identifier("id"),
	}
	args = append(args, workspaceID, object.Key)
	conditions = append(conditions, recordLocalizationQualifiedInClause(s, alias, "field_key", localized, &args))
	conditions = append(conditions, recordLocalizationQualifiedInClause(s, alias, "locale", recordmodel.RecordLocalizationLocales(query.Locale, query.FallbackLocale), &args))
	args = append(args, searchValue)
	conditions = append(conditions, "LOWER("+alias+"."+s.Identifier("text_value")+") LIKE "+s.Placeholder(len(args)))
	parts = append(parts, "EXISTS (SELECT 1 FROM "+s.TableIdentifier(recordLocalizedValueTable)+" AS "+alias+" WHERE "+strings.Join(conditions, " AND ")+")")
	return whereSQL + " AND (" + strings.Join(parts, " OR ") + ")", args, nil
}

func recordLocalizedOrder(s *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, placeholderStart int) (string, []any) {
	localizedSet := map[string]bool{}
	for _, field := range recordmodel.RecordLocalizedFieldKeys(object) {
		localizedSet[field] = true
	}
	parts := []string{}
	args := []any{}
	for index, rule := range query.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if direction != "DESC" {
			direction = "ASC"
		}
		if strings.TrimSpace(query.Locale) == "" || !localizedSet[rule.Field] {
			parts = append(parts, s.Identifier(rule.Field)+" "+direction)
			continue
		}
		values := []string{}
		for localeIndex, locale := range recordmodel.RecordLocalizationLocales(query.Locale, query.FallbackLocale) {
			alias := s.Identifier(fmt.Sprintf("record_i18n_sort_%d_%d", index, localeIndex))
			base := placeholderStart + len(args)
			values = append(values, "(SELECT "+alias+"."+s.Identifier("text_value")+" FROM "+s.TableIdentifier(recordLocalizedValueTable)+" AS "+alias+" WHERE "+alias+"."+s.Identifier("workspace_id")+" = "+s.Placeholder(base+1)+" AND "+alias+"."+s.Identifier("object_key")+" = "+s.Placeholder(base+2)+" AND "+alias+"."+s.Identifier("record_id")+" = "+s.TableIdentifier(object.Key)+"."+s.Identifier("id")+" AND "+alias+"."+s.Identifier("field_key")+" = "+s.Placeholder(base+3)+" AND "+alias+"."+s.Identifier("locale")+" = "+s.Placeholder(base+4)+" LIMIT 1)")
			args = append(args, workspaceID, object.Key, rule.Field, locale)
		}
		values = append(values, s.Identifier(rule.Field))
		parts = append(parts, "COALESCE("+strings.Join(values, ", ")+") "+direction)
	}
	if len(parts) == 0 {
		parts = append(parts, s.Identifier("id")+" ASC")
	}
	return " ORDER BY " + strings.Join(parts, ", "), args
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

func recordLocalizationInClause(s *database.RuntimeStore, field string, values []string, args *[]any) string {
	return recordLocalizationQualifiedInClause(s, "", field, values, args)
}

func recordLocalizationQualifiedInClause(s *database.RuntimeStore, alias, field string, values []string, args *[]any) string {
	placeholders := []string{}
	for _, value := range values {
		*args = append(*args, value)
		placeholders = append(placeholders, s.Placeholder(len(*args)))
	}
	identifier := s.Identifier(field)
	if alias != "" {
		identifier = alias + "." + identifier
	}
	if len(placeholders) == 0 {
		return "1 = 0"
	}
	return identifier + " IN (" + strings.Join(placeholders, ", ") + ")"
}
