package metadata

import (
	"context"
	"database/sql"
	"fmt"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

	"sort"
	"strings"
	"time"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func manifestLocalizedTextSeeds(seed manifestmodel.ManifestSchema) []metadatamodel.LocalizedText {
	sourceID := strings.TrimSpace(seed.TemplateID)
	if sourceID == "" {
		sourceID = "generated-template"
	}
	defaultLocale := manifestDefaultLocale(seed)
	out := []metadatamodel.LocalizedText{}
	addDefault := func(entityType string, entityKey string, property string, text string) {
		entityType = strings.TrimSpace(entityType)
		entityKey = strings.TrimSpace(entityKey)
		property = strings.TrimSpace(property)
		text = strings.TrimSpace(text)
		if entityKey == "" || text == "" {
			return
		}
		out = append(out, metadatamodel.LocalizedText{
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
				out = append(out, metadatamodel.LocalizedText{
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
	for _, view := range seed.Views {
		addDefault("view", view.Key, "name", view.Name)
		addI18n("view", view.Key, view.I18n)
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
	for _, entrypoint := range seed.EntryPoints {
		addDefault("entrypoint", entrypoint.Key, "name", entrypoint.Name)
		addDefault("entrypoint", entrypoint.Key, "description", entrypoint.Description)
		addI18n("entrypoint", entrypoint.Key, entrypoint.I18n)
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

func addValueOptionI18n(out *[]metadatamodel.LocalizedText, entityType string, parentKey string, value any, sourceID string) {
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
				*out = append(*out, metadatamodel.LocalizedText{
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

func addValueOptionDefaultTexts(out *[]metadatamodel.LocalizedText, entityType string, parentKey string, value any, locale string, sourceID string) {
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
			*out = append(*out, metadatamodel.LocalizedText{
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

func (s MetadataStore) syncManifestLocalizedTexts(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	for _, seed := range manifestLocalizedTextSeeds(manifest) {
		if err := s.syncLocalizedText(ctx, tx, seed, now); err != nil {
			return err
		}
	}
	return nil
}

func (s MetadataStore) syncLocalizedText(ctx context.Context, tx *sql.Tx, seed metadatamodel.LocalizedText, now string) error {
	seed = normalizeLocalizedText(seed)
	if seed.EntityType == "" || seed.EntityKey == "" || seed.Property == "" || seed.Locale == "" || seed.Text == "" {
		return nil
	}
	query := "SELECT " + strings.Join(quotedColumns(s.store, []string{"text", "source_kind"}), ", ") +
		" FROM " + s.store.TableIdentifier("business_localized_text") +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) +
		" AND " + s.store.Identifier("entity_type") + " = " + s.store.Placeholder(2) +
		" AND " + s.store.Identifier("entity_key") + " = " + s.store.Placeholder(3) +
		" AND " + s.store.Identifier("property") + " = " + s.store.Placeholder(4) +
		" AND " + s.store.Identifier("locale") + " = " + s.store.Placeholder(5)
	var currentText string
	var sourceKind string
	err := tx.QueryRowContext(ctx, query, seed.WorkspaceID, seed.EntityType, seed.EntityKey, seed.Property, seed.Locale).Scan(&currentText, &sourceKind)
	if err == sql.ErrNoRows {
		columns := []string{"id", "workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}
		values := []any{localizedTextID(seed), seed.WorkspaceID, seed.EntityType, seed.EntityKey, seed.Property, seed.Locale, seed.Text, seed.SourceKind, seed.SourceID, now, now}
		insert := "INSERT INTO " + s.store.TableIdentifier("business_localized_text") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
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
	update := "UPDATE " + s.store.TableIdentifier("business_localized_text") +
		" SET " + s.store.Identifier("text") + " = " + s.store.Placeholder(1) +
		", " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(2) +
		", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(4) +
		" AND " + s.store.Identifier("entity_type") + " = " + s.store.Placeholder(5) +
		" AND " + s.store.Identifier("entity_key") + " = " + s.store.Placeholder(6) +
		" AND " + s.store.Identifier("property") + " = " + s.store.Placeholder(7) +
		" AND " + s.store.Identifier("locale") + " = " + s.store.Placeholder(8)
	if _, err := tx.ExecContext(ctx, update, seed.Text, seed.SourceID, now, seed.WorkspaceID, seed.EntityType, seed.EntityKey, seed.Property, seed.Locale); err != nil {
		return fmt.Errorf("update localized text %s/%s/%s/%s: %w", seed.EntityType, seed.EntityKey, seed.Property, seed.Locale, err)
	}
	return nil
}

func (s MetadataStore) UpsertLocalizedText(ctx context.Context, workspaceID string, req metadatamodel.LocalizedTextUpsertRequest) (metadatamodel.LocalizedText, error) {
	workspaceID, err := requireMetadataWorkspaceID(workspaceID, req.WorkspaceID)
	if err != nil {
		return metadatamodel.LocalizedText{}, err
	}
	req.WorkspaceID = workspaceID
	text := normalizeLocalizedText(metadatamodel.LocalizedText{
		WorkspaceID: req.WorkspaceID,
		EntityType:  req.EntityType,
		EntityKey:   req.EntityKey,
		Property:    req.Property,
		Locale:      req.Locale,
		Text:        req.Text,
		SourceKind:  firstNonEmptyLocalizedText(req.SourceKind, "user"),
		SourceID:    firstNonEmptyLocalizedText(req.SourceID, "metadata_api"),
	})
	if text.EntityType == "" || text.EntityKey == "" || text.Property == "" || text.Locale == "" || text.Text == "" {
		return metadatamodel.LocalizedText{}, fmt.Errorf("localized text requires entity_type, entity_key, property, locale, and text")
	}
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return metadatamodel.LocalizedText{}, fmt.Errorf("begin localized text upsert: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.upsertLocalizedText(ctx, tx, text, now); err != nil {
		return metadatamodel.LocalizedText{}, err
	}
	if err := tx.Commit(); err != nil {
		return metadatamodel.LocalizedText{}, fmt.Errorf("commit localized text upsert: %w", err)
	}
	text.CreatedAt = now
	text.UpdatedAt = now
	return text, nil
}

func (s MetadataStore) upsertLocalizedText(ctx context.Context, tx *sql.Tx, text metadatamodel.LocalizedText, now string) error {
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("business_localized_text") +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) +
		" AND " + s.store.Identifier("entity_type") + " = " + s.store.Placeholder(2) +
		" AND " + s.store.Identifier("entity_key") + " = " + s.store.Placeholder(3) +
		" AND " + s.store.Identifier("property") + " = " + s.store.Placeholder(4) +
		" AND " + s.store.Identifier("locale") + " = " + s.store.Placeholder(5)
	var count int
	if err := tx.QueryRowContext(ctx, query, text.WorkspaceID, text.EntityType, text.EntityKey, text.Property, text.Locale).Scan(&count); err != nil {
		return fmt.Errorf("read localized text for upsert: %w", err)
	}
	if count == 0 {
		columns := []string{"id", "workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}
		values := []any{localizedTextID(text), text.WorkspaceID, text.EntityType, text.EntityKey, text.Property, text.Locale, text.Text, text.SourceKind, text.SourceID, now, now}
		insert := "INSERT INTO " + s.store.TableIdentifier("business_localized_text") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
			return fmt.Errorf("insert localized text: %w", err)
		}
		return nil
	}
	update := "UPDATE " + s.store.TableIdentifier("business_localized_text") +
		" SET " + s.store.Identifier("text") + " = " + s.store.Placeholder(1) +
		", " + s.store.Identifier("source_kind") + " = " + s.store.Placeholder(2) +
		", " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(3) +
		", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) +
		" WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) +
		" AND " + s.store.Identifier("entity_type") + " = " + s.store.Placeholder(6) +
		" AND " + s.store.Identifier("entity_key") + " = " + s.store.Placeholder(7) +
		" AND " + s.store.Identifier("property") + " = " + s.store.Placeholder(8) +
		" AND " + s.store.Identifier("locale") + " = " + s.store.Placeholder(9)
	if _, err := tx.ExecContext(ctx, update, text.Text, text.SourceKind, text.SourceID, now, text.WorkspaceID, text.EntityType, text.EntityKey, text.Property, text.Locale); err != nil {
		return fmt.Errorf("update localized text: %w", err)
	}
	return nil
}

func (s MetadataStore) ListLocalizedTexts(ctx context.Context, workspaceID string, query metadatamodel.LocalizedTextQuery) ([]metadatamodel.LocalizedText, error) {
	workspaceID, err := requireMetadataWorkspaceID(workspaceID, query.WorkspaceID)
	if err != nil {
		return nil, err
	}
	query.WorkspaceID = workspaceID
	clauses := []string{s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1)}
	args := []any{workspaceID}
	add := func(column string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		args = append(args, value)
		clauses = append(clauses, s.store.Identifier(column)+" = "+s.store.Placeholder(len(args)))
	}
	add("entity_type", query.EntityType)
	add("entity_key", query.EntityKey)
	add("property", query.Property)
	add("locale", query.Locale)
	sqlQuery := "SELECT " + strings.Join(quotedColumns(s.store, []string{"workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}), ", ") +
		" FROM " + s.store.TableIdentifier("business_localized_text") +
		" WHERE " + strings.Join(clauses, " AND ") +
		" ORDER BY " + s.store.Identifier("entity_type") + ", " + s.store.Identifier("entity_key") + ", " + s.store.Identifier("property") + ", " + s.store.Identifier("locale")
	rows, err := s.database().QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list localized texts: %w", err)
	}
	defer rows.Close()
	out := []metadatamodel.LocalizedText{}
	for rows.Next() {
		var item metadatamodel.LocalizedText
		if err := rows.Scan(&item.WorkspaceID, &item.EntityType, &item.EntityKey, &item.Property, &item.Locale, &item.Text, &item.SourceKind, &item.SourceID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan localized text: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func normalizeLocalizedText(text metadatamodel.LocalizedText) metadatamodel.LocalizedText {
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

func localizedTextID(text metadatamodel.LocalizedText) string {
	return strings.Join([]string{text.WorkspaceID, text.EntityType, text.EntityKey, text.Property, text.Locale}, ":")
}
