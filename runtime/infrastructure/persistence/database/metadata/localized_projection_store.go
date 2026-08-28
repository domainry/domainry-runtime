package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataLocalizedProjection struct {
	Locale   string
	Property string
	Text     string
}

func (s MetadataStore) syncMetadataLocalizedTextTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, payload []byte, sourceID, now string) error {
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
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.store.TableIdentifier("business_localized_text")+" WHERE "+s.store.Identifier("workspace_id")+" = "+s.store.Placeholder(1)+" AND "+s.store.Identifier("entity_type")+" = "+s.store.Placeholder(2)+" AND "+s.store.Identifier("entity_key")+" = "+s.store.Placeholder(3)+" AND "+s.store.Identifier("source_kind")+" = "+s.store.Placeholder(4), workspaceID, entityType, entityKey, "metadata_definition"); err != nil {
		return fmt.Errorf("clear metadata localized text projection: %w", err)
	}
	for _, projection := range projections {
		update := "UPDATE " + s.store.TableIdentifier("business_localized_text") + " SET " + s.store.Identifier("text") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("source_kind") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("entity_type") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("entity_key") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("property") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("locale") + " = " + s.store.Placeholder(9)
		result, err := tx.ExecContext(ctx, update, projection.Text, "metadata_definition", sourceID, now, workspaceID, entityType, entityKey, projection.Property, projection.Locale)
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
		localized := metadatamodel.LocalizedText{WorkspaceID: workspaceID, EntityType: entityType, EntityKey: entityKey, Property: projection.Property, Locale: projection.Locale, Text: projection.Text}
		columns := []string{"id", "workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}
		values := []any{localizedTextID(localized), workspaceID, entityType, entityKey, projection.Property, projection.Locale, projection.Text, "metadata_definition", sourceID, now, now}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.store.TableIdentifier("business_localized_text")+" ("+joinIdentifiers(s.store, columns...)+") VALUES ("+joinPlaceholders(s.store, len(values))+")", values...); err != nil {
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
