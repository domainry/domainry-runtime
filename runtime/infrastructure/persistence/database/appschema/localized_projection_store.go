package appschema

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataLocalizedProjection struct {
	Locale   string
	Property string
	Text     string
}

func (s ApplicationSchemaStore) syncMetadataLocalizedTextTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, payload []byte, sourceID, now string) error {
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return fmt.Errorf("decode metadata localized text projection: %w", err)
	}
	rawI18n, declared := document["i18n"]
	if !declared {
		return nil
	}
	projections := metadataLocalizedProjections(rawI18n)
	workspaceID := principalmodel.InstallationWorkspaceID
	entityType, entityKey := strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	deleteQuery, deleteArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", workspaceID).Where(ormbuilder.And(ormbuilder.Equal("entity_type", entityType), ormbuilder.Equal("entity_key", entityKey), ormbuilder.Equal("source_kind", "metadata_definition"))).Build()
	if buildErr != nil {
		return fmt.Errorf("build metadata localized text projection clear: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		return fmt.Errorf("clear metadata localized text projection: %w", err)
	}
	for _, projection := range projections {
		update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", workspaceID).
			Set("text", projection.Text).Set("source_kind", "metadata_definition").Set("source_id", sourceID).Set("updated_at", now).
			Where(ormbuilder.And(ormbuilder.Equal("entity_type", entityType), ormbuilder.Equal("entity_key", entityKey), ormbuilder.Equal("property", projection.Property), ormbuilder.Equal("locale", projection.Locale))).Build()
		if buildErr != nil {
			return fmt.Errorf("build metadata localized text projection update: %w", buildErr)
		}
		result, err := tx.ExecContext(ctx, update, updateArgs...)
		if err != nil {
			return fmt.Errorf("update metadata localized text projection: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read updated metadata localized text projection rows: %w", err)
		}
		if affected == 1 {
			continue
		}
		localized := appschemamodel.LocalizedText{WorkspaceID: workspaceID, EntityType: entityType, EntityKey: entityKey, Property: projection.Property, Locale: projection.Locale, Text: projection.Text}
		insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_application_schema_localized_texts", workspaceID).
			Columns("id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at").
			Values(localizedTextID(localized), entityType, entityKey, projection.Property, projection.Locale, projection.Text, "metadata_definition", sourceID, now, now).Build()
		if buildErr != nil {
			return fmt.Errorf("build metadata localized text projection insert: %w", buildErr)
		}
		if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
			return fmt.Errorf("insert metadata localized text projection: %w", err)
		}
	}
	return nil
}

func metadataLocalizedProjections(value any) []metadataLocalizedProjection {
	locales, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := []metadataLocalizedProjection{}
	for locale, rawProperties := range locales {
		properties, ok := rawProperties.(map[string]any)
		if !ok {
			continue
		}
		for property, rawText := range properties {
			text, ok := rawText.(string)
			if !ok || strings.TrimSpace(locale) == "" || strings.TrimSpace(property) == "" || strings.TrimSpace(text) == "" {
				continue
			}
			result = append(result, metadataLocalizedProjection{Locale: strings.TrimSpace(locale), Property: strings.TrimSpace(property), Text: text})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Locale+"\x00"+result[i].Property < result[j].Locale+"\x00"+result[j].Property
	})
	return result
}
