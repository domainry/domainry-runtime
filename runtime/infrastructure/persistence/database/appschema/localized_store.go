package appschema

import (
	"context"
	"database/sql"
	"fmt"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

	"sort"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func manifestLocalizedTextSeeds(seed manifestmodel.ManifestSchema) []appschemamodel.LocalizedText {
	sourceID := strings.TrimSpace(seed.TemplateID)
	if sourceID == "" {
		sourceID = "generated-template"
	}
	defaultLocale := manifestDefaultLocale(seed)
	out := []appschemamodel.LocalizedText{}
	addDefault := func(entityType string, entityKey string, property string, text string) {
		entityType = strings.TrimSpace(entityType)
		entityKey = strings.TrimSpace(entityKey)
		property = strings.TrimSpace(property)
		text = strings.TrimSpace(text)
		if entityKey == "" || text == "" {
			return
		}
		out = append(out, appschemamodel.LocalizedText{
			WorkspaceID: principalmodel.InstallationWorkspaceID,
			EntityType:  entityType,
			EntityKey:   entityKey,
			Property:    property,
			Locale:      defaultLocale,
			Text:        text,
			SourceKind:  "generated",
			SourceID:    sourceID,
		})
	}
	addI18n := func(entityType string, entityKey string, values localizationmodel.LocalizedTextMap) {
		entityType = strings.TrimSpace(entityType)
		entityKey = strings.TrimSpace(entityKey)
		if entityKey == "" || len(values) == 0 {
			return
		}
		for locale, properties := range values {
			locale = strings.TrimSpace(locale)
			if locale == "" {
				continue
			}
			for property, text := range properties {
				property = strings.TrimSpace(property)
				text = strings.TrimSpace(text)
				if property == "" || text == "" {
					continue
				}
				out = append(out, appschemamodel.LocalizedText{
					WorkspaceID: principalmodel.InstallationWorkspaceID,
					EntityType:  entityType,
					EntityKey:   entityKey,
					Property:    property,
					Locale:      locale,
					Text:        text,
					SourceKind:  "generated",
					SourceID:    sourceID,
				})
			}
		}
	}
	addDefault("app", "app", "name", seed.Name)
	addDefault("app", "app", "description", seed.Description)
	addI18n("app", "app", seed.I18n)
	for _, object := range seed.Objects {
		addDefault("object", object.Key, "name", object.Name)
		addDefault("object", object.Key, "description", object.Description)
		addI18n("object", object.Key, object.I18n)
		for _, field := range object.Fields {
			fieldKey := metadataJoinedKey(object.Key, field.Key)
			addDefault("field", fieldKey, "name", field.Name)
			addI18n("field", fieldKey, field.I18n)
			addValueOptionDefaultTexts(&out, "field_option", fieldKey, field.Options, defaultLocale, sourceID)
			addValueOptionI18n(&out, "field_option", fieldKey, field.Options, sourceID)
		}
		for index, validation := range object.Validations {
			key := validation.Key
			if strings.TrimSpace(key) == "" {
				key = validationMetadataKey(object.Key, index, validation)
			}
			addDefault("validation", key, "message", validation.Message)
			addI18n("validation", key, validation.I18n)
		}
	}
	for _, action := range seed.Actions {
		addDefault("action", action.Key, "label", action.Label)
		addI18n("action", action.Key, action.I18n)
		for _, field := range action.PayloadFields {
			addDefault("action_payload_field", metadataJoinedKey(action.Key, field.Key), "name", field.Name)
			addI18n("action_payload_field", metadataJoinedKey(action.Key, field.Key), field.I18n)
		}
	}
	for _, workflow := range seed.Workflows {
		addDefault("workflow", workflow.Key, "name", workflow.Name)
		addI18n("workflow", workflow.Key, workflow.I18n)
	}
	for _, dictionary := range seed.Dictionaries {
		addDefault("dictionary", dictionary.Key, "name", dictionary.Name)
		addDefault("dictionary", dictionary.Key, "description", dictionary.Description)
		addI18n("dictionary", dictionary.Key, dictionary.I18n)
		for _, item := range dictionary.Items {
			itemKey := metadataJoinedKey(dictionary.Key, firstNonEmptyLocalizedText(item.Key, item.Value))
			addDefault("dictionary_item", itemKey, "label", item.Label)
			addDefault("dictionary_item", itemKey, "description", item.Description)
			addI18n("dictionary_item", itemKey, item.I18n)
		}
	}
	for _, report := range seed.Reports {
		addDefault("report", report.Key, "name", report.Name)
		addI18n("report", report.Key, report.I18n)
	}
	for _, example := range seed.OperationStateExamples {
		addDefault("operation_state_example", example.Key, "name", example.Name)
		addI18n("operation_state_example", example.Key, example.I18n)
	}
	for _, policy := range seed.SensitiveFieldPolicies {
		addDefault("sensitive_field_policy", policy.Key, "name", policy.Name)
		addI18n("sensitive_field_policy", policy.Key, policy.I18n)
	}
	for _, control := range seed.ReportExportControls {
		addDefault("report_export_control", control.Key, "name", control.Name)
		addI18n("report_export_control", control.Key, control.I18n)
	}
	for _, skill := range seed.Skills {
		addDefault("skill", skill.Key, "name", skill.Name)
		addDefault("skill", skill.Key, "description", skill.Description)
		addI18n("skill", skill.Key, skill.I18n)
	}
	for _, agent := range seed.Agents {
		addDefault("agent", agent.Key, "name", agent.Name)
		addDefault("agent", agent.Key, "description", agent.Description)
		addI18n("agent", agent.Key, agent.I18n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		for _, less := range []int{
			strings.Compare(out[i].EntityType, out[j].EntityType),
			strings.Compare(out[i].EntityKey, out[j].EntityKey),
			strings.Compare(out[i].Property, out[j].Property),
			strings.Compare(out[i].Locale, out[j].Locale),
		} {
			if less < 0 {
				return true
			}
			if less > 0 {
				return false
			}
		}
		return false
	})
	return out
}

func manifestDefaultLocale(seed manifestmodel.ManifestSchema) string {
	if locale := strings.TrimSpace(seed.DefaultLocale); locale != "" {
		return locale
	}
	return "en-US"
}

func addValueOptionI18n(out *[]appschemamodel.LocalizedText, entityType string, parentKey string, value any, sourceID string) {
	for _, option := range localizedValueOptionMaps(value) {
		key := strings.TrimSpace(fmt.Sprint(option["value"]))
		if key == "" || key == "<nil>" {
			key = strings.TrimSpace(fmt.Sprint(option["key"]))
		}
		i18n, ok := option["i18n"].(map[string]any)
		if !ok || key == "" || key == "<nil>" {
			continue
		}
		for locale, propertiesAny := range i18n {
			properties, ok := propertiesAny.(map[string]any)
			if !ok {
				continue
			}
			for property, textAny := range properties {
				text := strings.TrimSpace(fmt.Sprint(textAny))
				if strings.TrimSpace(locale) == "" || strings.TrimSpace(property) == "" || text == "" || text == "<nil>" {
					continue
				}
				*out = append(*out, appschemamodel.LocalizedText{
					WorkspaceID: principalmodel.InstallationWorkspaceID,
					EntityType:  entityType,
					EntityKey:   metadataJoinedKey(parentKey, key),
					Property:    strings.TrimSpace(property),
					Locale:      strings.TrimSpace(locale),
					Text:        text,
					SourceKind:  "generated",
					SourceID:    sourceID,
				})
			}
		}
	}
}

func addValueOptionDefaultTexts(out *[]appschemamodel.LocalizedText, entityType string, parentKey string, value any, locale string, sourceID string) {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return
	}
	for _, option := range localizedValueOptionMaps(value) {
		key := strings.TrimSpace(fmt.Sprint(option["value"]))
		if key == "" || key == "<nil>" {
			key = strings.TrimSpace(fmt.Sprint(option["key"]))
		}
		if key == "" || key == "<nil>" {
			continue
		}
		for _, property := range []string{"label", "description"} {
			text := strings.TrimSpace(fmt.Sprint(option[property]))
			if text == "" || text == "<nil>" {
				continue
			}
			*out = append(*out, appschemamodel.LocalizedText{
				WorkspaceID: principalmodel.InstallationWorkspaceID,
				EntityType:  entityType,
				EntityKey:   metadataJoinedKey(parentKey, key),
				Property:    property,
				Locale:      locale,
				Text:        text,
				SourceKind:  "generated",
				SourceID:    sourceID,
			})
		}
	}
}

func localizedValueOptionMaps(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			if option, ok := item.(map[string]any); ok {
				out = append(out, option)
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmptyLocalizedText(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func (s ApplicationSchemaStore) syncManifestLocalizedTexts(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	for _, seed := range manifestLocalizedTextSeeds(manifest) {
		if err := s.syncLocalizedText(ctx, tx, seed, now); err != nil {
			return err
		}
	}
	return nil
}

func (s ApplicationSchemaStore) syncLocalizedText(ctx context.Context, tx *sql.Tx, seed appschemamodel.LocalizedText, now string) error {
	seed = normalizeLocalizedText(seed)
	if seed.EntityType == "" || seed.EntityKey == "" || seed.Property == "" || seed.Locale == "" || seed.Text == "" {
		return nil
	}
	predicate := localizedTextIdentityPredicate(seed.EntityType, seed.EntityKey, seed.Property, seed.Locale)
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", seed.WorkspaceID).Columns("text", "source_kind").Where(predicate).Build()
	if buildErr != nil {
		return fmt.Errorf("build localized text lookup: %w", buildErr)
	}
	var currentText string
	var sourceKind string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&currentText, &sourceKind)
	if err == sql.ErrNoRows {
		insert, insertArgs, buildErr := localizedTextInsertBuilder(s, seed, now).Build()
		if buildErr != nil {
			return fmt.Errorf("build localized text insert: %w", buildErr)
		}
		if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
			return fmt.Errorf("insert localized text %s/%s/%s/%s: %w", seed.EntityType, seed.EntityKey, seed.Property, seed.Locale, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read localized text %s/%s/%s/%s: %w", seed.EntityType, seed.EntityKey, seed.Property, seed.Locale, err)
	}
	if strings.TrimSpace(sourceKind) != "generated" || currentText == seed.Text {
		return nil
	}
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", seed.WorkspaceID).Set("text", seed.Text).Set("source_id", seed.SourceID).Set("updated_at", now).Where(predicate).Build()
	if buildErr != nil {
		return fmt.Errorf("build localized text update: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
		return fmt.Errorf("update localized text %s/%s/%s/%s: %w", seed.EntityType, seed.EntityKey, seed.Property, seed.Locale, err)
	}
	return nil
}

func (s ApplicationSchemaStore) upsertLocalizedText(ctx context.Context, tx *sql.Tx, text appschemamodel.LocalizedText, now string) error {
	predicate := localizedTextIdentityPredicate(text.EntityType, text.EntityKey, text.Property, text.Locale)
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", text.WorkspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(predicate).Build()
	if buildErr != nil {
		return fmt.Errorf("build localized text upsert lookup: %w", buildErr)
	}
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return fmt.Errorf("read localized text for upsert: %w", err)
	}
	if count == 0 {
		insert, insertArgs, buildErr := localizedTextInsertBuilder(s, text, now).Build()
		if buildErr != nil {
			return fmt.Errorf("build localized text insert: %w", buildErr)
		}
		if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
			return fmt.Errorf("insert localized text: %w", err)
		}
		return nil
	}
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", text.WorkspaceID).Set("text", text.Text).Set("source_kind", text.SourceKind).Set("source_id", text.SourceID).Set("updated_at", now).Where(predicate).Build()
	if buildErr != nil {
		return fmt.Errorf("build localized text update: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
		return fmt.Errorf("update localized text: %w", err)
	}
	return nil
}

func (s ApplicationSchemaStore) ListLocalizedTexts(ctx context.Context, workspaceID string, query appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	workspaceID, err := requireMetadataWorkspaceID(workspaceID, query.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query.WorkspaceID = workspaceID
	predicates := []ormbuilder.Predicate{}
	add := func(column string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		predicates = append(predicates, ormbuilder.Equal(column, value))
	}
	add("entity_type", query.EntityType)
	add("entity_key", query.EntityKey)
	add("property", query.Property)
	add("locale", query.Locale)
	selectBuilder := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", workspaceID).Columns("workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at").OrderBy(ormbuilder.Ascending("entity_type"), ormbuilder.Ascending("entity_key"), ormbuilder.Ascending("property"), ormbuilder.Ascending("locale"))
	if len(predicates) > 0 {
		selectBuilder.Where(ormbuilder.And(predicates...))
	}
	sqlQuery, args, buildErr := selectBuilder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build localized text list: %w", buildErr)
	}
	rows, err := s.database().QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list localized texts: %w", err)
	}
	defer rows.Close()
	out := []appschemamodel.LocalizedText{}
	for rows.Next() {
		var item appschemamodel.LocalizedText
		if err := rows.Scan(&item.WorkspaceID, &item.EntityType, &item.EntityKey, &item.Property, &item.Locale, &item.Text, &item.SourceKind, &item.SourceID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan localized text: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func localizedTextIdentityPredicate(entityType, entityKey, property, locale string) ormbuilder.Predicate {
	return ormbuilder.And(
		ormbuilder.Equal("entity_type", entityType),
		ormbuilder.Equal("entity_key", entityKey),
		ormbuilder.Equal("property", property),
		ormbuilder.Equal("locale", locale),
	)
}

func localizedTextInsertBuilder(s ApplicationSchemaStore, text appschemamodel.LocalizedText, now string) *ormbuilder.InsertBuilder {
	return ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", text.WorkspaceID).
		Columns("id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at").
		Values(localizedTextID(text), text.EntityType, text.EntityKey, text.Property, text.Locale, text.Text, text.SourceKind, text.SourceID, now, now)
}

func normalizeLocalizedText(text appschemamodel.LocalizedText) appschemamodel.LocalizedText {
	text.WorkspaceID = strings.TrimSpace(text.WorkspaceID)
	text.EntityType = strings.TrimSpace(text.EntityType)
	text.EntityKey = strings.TrimSpace(text.EntityKey)
	text.Property = strings.TrimSpace(text.Property)
	text.Locale = strings.TrimSpace(text.Locale)
	text.Text = strings.TrimSpace(text.Text)
	text.SourceKind = firstNonEmptyLocalizedText(text.SourceKind, "generated")
	text.SourceID = firstNonEmptyLocalizedText(text.SourceID, "generated-template")
	return text
}

func localizedTextID(text appschemamodel.LocalizedText) string {
	return strings.Join([]string{text.WorkspaceID, text.EntityType, text.EntityKey, text.Property, text.Locale}, ":")
}
