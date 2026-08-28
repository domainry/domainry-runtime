package metadata

import auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"strings"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// PublishDefinition commits the active definition, immutable version, Audit,
// and active catalog revision as one local publication fact.
func (r MetadataStore) PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	return r.publishDefinition(ctx, scope, resourceType, resourceKey, req, &audit)
}

func (r MetadataStore) publishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest, audit *auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if len(req.Payload) == 0 {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("metadata payload is required")
	}
	shape, err := metadataDefinitionShape(ctx, resourceType, resourceKey, req)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	raw, hash, _ := metadataPayload(shape.Payload)
	if replay, found, replayErr := r.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr != nil {
		return metadatamodel.MetadataDefinition{}, replayErr
	} else if found {
		return replay, nil
	}
	sourceKind := metadataMutationValueOrDefault(req.SourceKind, "user")
	sourceID := metadataMutationValueOrDefault(req.SourceID, "metadata_api")
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("begin metadata upsert: %w", err)
	}
	defer tx.Rollback()
	if err := r.replaceDefinition(ctx, tx, table, resourceType, shape.Key, req.ExpectedSchemaHash); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	version, err := r.nextVersion(ctx, tx, resourceType, shape.Key)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
	values := []any{metadataResourceID(resourceType, shape.Key), shape.Key, shape.ObjectKey, shape.Name, string(raw), version, hash, sourceKind, sourceID, nil, now, now}
	query := "INSERT INTO " + r.store.TableIdentifier(table) + " (" + strings.Join(database.QuotedColumns(r.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(r.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		_ = tx.Rollback()
		if replay, found, replayErr := r.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("insert %s %s: %w", resourceType, shape.Key, err)
	}
	if err := r.insertDefinitionVersion(ctx, tx, resourceType, shape.Key, version, hash, raw, now); err != nil {
		_ = tx.Rollback()
		if replay, found, replayErr := r.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, err
	}
	definition := metadatamodel.MetadataDefinition{ResourceType: resourceType, ResourceKey: shape.Key, ObjectKey: shape.ObjectKey, Name: shape.Name, Payload: append([]byte(nil), raw...), SchemaVersion: version, SchemaHash: hash, SourceKind: sourceKind, SourceID: sourceID, CreatedAt: now, UpdatedAt: now}
	if audit != nil {
		audit.After = metadataDefinitionAuditValue(definition)
		if audit.Metadata == nil {
			audit.Metadata = map[string]any{}
		}
		audit.Metadata["schema_version"], audit.Metadata["schema_hash"] = version, hash
		if err := r.insertChangeAudit(ctx, tx, *audit); err != nil {
			return metadatamodel.MetadataDefinition{}, err
		}
	}
	if err := r.insertDefinitionRefreshIntentTx(ctx, tx, definition, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.refreshCatalogHashTx(ctx, tx, now); err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("refresh active metadata revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		if replay, found, replayErr := r.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("commit metadata upsert: %w", err)
	}
	return definition, nil
}

func (r MetadataStore) insertDefinitionRefreshIntentTx(ctx context.Context, tx *sql.Tx, definition metadatamodel.MetadataDefinition, now string) error {
	payload, _ := json.Marshal(map[string]any{"resource_type": definition.ResourceType, "resource_key": definition.ResourceKey, "schema_version": definition.SchemaVersion, "schema_hash": definition.SchemaHash})
	id := metadataDefinitionRefreshIntentID(definition.ResourceType, definition.ResourceKey, definition.SchemaHash)
	leaseExpires := time.Now().UTC().Add(90 * time.Second).Format(time.RFC3339Nano)
	columns := []string{"id", "workspace_id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"}
	values := []any{id, principalmodel.InstallationWorkspaceID, "metadata", "runtime_refresh", definition.ResourceType + ":" + definition.ResourceKey, definition.SchemaHash, "executing", string(payload), "{}", 0, "", "metadata-inline", leaseExpires, 1, "", now, now}
	query := "INSERT INTO " + r.store.TableIdentifier("transaction_boundary_intents") + " (" + stringsJoinIdentifiers(r.store, columns...) + ") VALUES (" + strings.Join(placeholders(r.store, len(values)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert metadata refresh intent: %w", err)
	}
	return nil
}

func (r MetadataStore) CompleteDefinitionRefresh(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, schemaHash, errorText string) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	id := metadataDefinitionRefreshIntentID(strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey), strings.TrimSpace(schemaHash))
	status, nextAttemptAt, attemptIncrement := "succeeded", "", 0
	if strings.TrimSpace(errorText) != "" {
		status = "reconciliation_required"
		nextAttemptAt = time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
		attemptIncrement = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := "UPDATE " + r.store.TableIdentifier("transaction_boundary_intents") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("last_error") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("next_attempt_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("attempt_count") + " = " + r.store.Identifier("attempt_count") + " + " + r.store.Placeholder(4) + ", " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(5) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("status") + " = 'executing' AND " + r.store.Identifier("lease_owner") + " = 'metadata-inline' AND " + r.store.Identifier("fencing_token") + " = 1"
	result, err := r.database().ExecContext(ctx, query, status, strings.TrimSpace(errorText), nextAttemptAt, attemptIncrement, now, id)
	if err != nil {
		return fmt.Errorf("complete metadata refresh intent: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed metadata refresh intent rows: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("metadata refresh intent transition conflict")
	}
	return nil
}

func metadataDefinitionAuditValue(definition metadatamodel.MetadataDefinition) map[string]any {
	value := map[string]any{"resource_type": definition.ResourceType, "resource_key": definition.ResourceKey, "schema_version": definition.SchemaVersion, "schema_hash": definition.SchemaHash, "source_kind": definition.SourceKind, "source_id": definition.SourceID}
	var payload any
	if json.Unmarshal(definition.Payload, &payload) == nil {
		value["payload"] = payload
	}
	return value
}

func (r MetadataStore) DisableDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return err
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return fmt.Errorf("begin disable %s %s: %w", resourceType, resourceKey, err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier(table)+" SET "+r.store.Identifier("disabled_at")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("resource_key")+" = "+r.store.Placeholder(2), now, resourceKey)
	if err != nil {
		return fmt.Errorf("disable %s %s: %w", resourceType, resourceKey, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read disabled %s %s rows: %w", resourceType, resourceKey, err)
	}
	if rows == 0 {
		return fmt.Errorf("metadata.%s.notFound: %s", resourceType, resourceKey)
	}
	if err := r.refreshCatalogHashTx(ctx, tx, now); err != nil {
		return fmt.Errorf("refresh metadata catalog hash while disabling %s %s: %w", resourceType, resourceKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit disable %s %s: %w", resourceType, resourceKey, err)
	}
	return nil
}

func (r MetadataStore) replaceDefinition(ctx context.Context, tx *sql.Tx, table, resourceType, resourceKey string, expectedHash *string) error {
	keyColumn, hashColumn := r.store.Identifier("resource_key"), r.store.Identifier("schema_hash")
	if expectedHash == nil {
		_, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+r.store.Placeholder(1), resourceKey)
		if err != nil {
			return fmt.Errorf("replace %s %s: %w", resourceType, resourceKey, err)
		}
		return nil
	}
	expected := strings.TrimSpace(*expectedHash)
	if expected == "" {
		var current string
		err := tx.QueryRowContext(ctx, "SELECT "+hashColumn+" FROM "+r.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+r.store.Placeholder(1), resourceKey).Scan(&current)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current %s %s version: %w", resourceType, resourceKey, err)
		}
		return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+r.store.Placeholder(1)+" AND "+hashColumn+" = "+r.store.Placeholder(2), resourceKey, expected)
	if err != nil {
		return fmt.Errorf("replace %s %s: %w", resourceType, resourceKey, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read replaced %s %s rows: %w", resourceType, resourceKey, err)
	}
	if affected == 1 {
		return nil
	}
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT "+hashColumn+" FROM "+r.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+r.store.Placeholder(1), resourceKey).Scan(&current); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read conflicting %s %s version: %w", resourceType, resourceKey, err)
	}
	return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
}

func (r MetadataStore) nextVersion(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string) (string, error) {
	query := "SELECT COUNT(*) FROM " + r.store.TableIdentifier("metadata_definition_versions") + " WHERE " + r.store.Identifier("resource_type") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("resource_key") + " = " + r.store.Placeholder(2)
	var count int
	if err := tx.QueryRowContext(ctx, query, resourceType, resourceKey).Scan(&count); err != nil {
		return "", fmt.Errorf("read metadata version count: %w", err)
	}
	return fmt.Sprintf("%d", count+1), nil
}

func (r MetadataStore) insertDefinitionVersion(ctx context.Context, tx *sql.Tx, resourceType, resourceKey, version, hash string, payload []byte, now string) error {
	columns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	values := []any{metadataResourceID(resourceType+":version", resourceKey+":"+version+":"+metadataHashPrefix(hash)), resourceType, resourceKey, version, hash, string(payload), now}
	query := "INSERT INTO " + r.store.TableIdentifier("metadata_definition_versions") + " (" + strings.Join(database.QuotedColumns(r.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(r.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert %s %s version: %w", resourceType, resourceKey, err)
	}
	return nil
}

func (r MetadataStore) ApplyDefinitionMutations(ctx context.Context, scope principalmodel.SystemScope, mutations []metadatamodel.MetadataDefinitionMutation, audits []auditmodel.AuditEvent, publication *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return nil, fmt.Errorf("begin metadata change plan: %w", err)
	}
	defer tx.Rollback()
	definitions := make([]metadatamodel.MetadataDefinition, 0, len(mutations))
	for _, mutation := range mutations {
		var definition metadatamodel.MetadataDefinition
		switch mutation.Operation {
		case "create", "update":
			definition, err = r.applyDefinitionUpsert(ctx, tx, mutation)
		case "archive", "delete":
			definition, err = r.applyDefinitionArchive(ctx, tx, mutation)
		case "noop":
			continue
		default:
			err = fmt.Errorf("metadata change operation is unsupported: %s", mutation.Operation)
		}
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	for _, audit := range audits {
		if err := r.insertChangeAudit(ctx, tx, audit); err != nil {
			return nil, err
		}
	}
	if err := r.refreshCatalogHashTx(ctx, tx, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, fmt.Errorf("refresh metadata catalog hash: %w", err)
	}
	if publication != nil && publication.ExpectedRevision > 0 {
		workspaceID, scopeErr := principalmodel.NewWorkspaceID(publication.WorkspaceID)
		if scopeErr != nil {
			return nil, scopeErr
		}
		query := "UPDATE " + r.store.TableIdentifier("business_change_plan_drafts") + " SET " + r.store.Identifier("status") + " = 'applying', " + r.store.Identifier("updated_by") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("revision") + " = " + r.store.Identifier("revision") + " + 1 WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = 'approved'"
		result, updateErr := tx.ExecContext(ctx, query, publication.UpdatedBy, publication.UpdatedAt, workspaceID.String(), publication.PlanID, publication.ExpectedRevision)
		if updateErr != nil {
			return nil, fmt.Errorf("publish domain change plan draft: %w", updateErr)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, fmt.Errorf("read published domain change plan draft rows: %w", rowsErr)
		}
		if affected != 1 {
			return nil, &changeplanmodel.BusinessChangePlanDraftConflictError{PlanID: publication.PlanID}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit metadata change plan: %w", err)
	}
	return definitions, nil
}

func (r MetadataStore) applyDefinitionUpsert(ctx context.Context, tx *sql.Tx, mutation metadatamodel.MetadataDefinitionMutation) (metadatamodel.MetadataDefinition, error) {
	table, err := metadataDefinitionTable(mutation.ResourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	shape, err := metadataDefinitionShape(ctx, mutation.ResourceType, mutation.ResourceKey, mutation.Request)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	raw, hash, _ := metadataPayload(shape.Payload)
	if err := r.replaceDefinition(ctx, tx, table, mutation.ResourceType, shape.Key, mutation.Request.ExpectedSchemaHash); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	version, err := r.nextVersion(ctx, tx, mutation.ResourceType, shape.Key)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sourceKind := metadataMutationValueOrDefault(mutation.Request.SourceKind, "builder")
	sourceID := metadataMutationValueOrDefault(mutation.Request.SourceID, "business_change_plan")
	columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
	values := []any{metadataResourceID(mutation.ResourceType, shape.Key), shape.Key, shape.ObjectKey, shape.Name, string(raw), version, hash, sourceKind, sourceID, nil, now, now}
	query := "INSERT INTO " + r.store.TableIdentifier(table) + " (" + strings.Join(database.QuotedColumns(r.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(r.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("insert %s %s: %w", mutation.ResourceType, shape.Key, err)
	}
	if err := r.insertDefinitionVersion(ctx, tx, mutation.ResourceType, shape.Key, version, hash, raw, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.syncLocalizedProjection(ctx, tx, mutation.ResourceType, shape.Key, raw, sourceID, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	return metadatamodel.MetadataDefinition{ResourceType: mutation.ResourceType, ResourceKey: shape.Key, ObjectKey: shape.ObjectKey, Name: shape.Name, Payload: raw, SchemaVersion: version, SchemaHash: hash, SourceKind: sourceKind, SourceID: sourceID, CreatedAt: now, UpdatedAt: now}, nil
}

func (r MetadataStore) applyDefinitionArchive(ctx context.Context, tx *sql.Tx, mutation metadatamodel.MetadataDefinitionMutation) (metadatamodel.MetadataDefinition, error) {
	table, err := metadataDefinitionTable(mutation.ResourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if mutation.Request.ExpectedSchemaHash == nil || strings.TrimSpace(*mutation.Request.ExpectedSchemaHash) == "" {
		return metadatamodel.MetadataDefinition{}, &metadatamodel.MetadataDefinitionConflictError{ResourceType: mutation.ResourceType, ResourceKey: mutation.ResourceKey}
	}
	expected, now := strings.TrimSpace(*mutation.Request.ExpectedSchemaHash), time.Now().UTC().Format(time.RFC3339)
	query := "UPDATE " + r.store.TableIdentifier(table) + " SET " + r.store.Identifier("disabled_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("resource_key") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("schema_hash") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("disabled_at") + " IS NULL"
	result, err := tx.ExecContext(ctx, query, now, now, mutation.ResourceKey, expected)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("archive %s %s: %w", mutation.ResourceType, mutation.ResourceKey, err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("read archived %s %s rows: %w", mutation.ResourceType, mutation.ResourceKey, rowsErr)
	}
	if affected != 1 {
		return metadatamodel.MetadataDefinition{}, &metadatamodel.MetadataDefinitionConflictError{ResourceType: mutation.ResourceType, ResourceKey: mutation.ResourceKey, ExpectedHash: expected, CurrentHash: r.currentHash(ctx, tx, table, mutation.ResourceKey)}
	}
	return metadatamodel.MetadataDefinition{ResourceType: mutation.ResourceType, ResourceKey: mutation.ResourceKey, SchemaHash: expected, DisabledAt: now, UpdatedAt: now}, nil
}

func (r MetadataStore) insertChangeAudit(ctx context.Context, tx *sql.Tx, event auditmodel.AuditEvent) error {
	before, err := json.Marshal(event.Before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	after, err := json.Marshal(event.After)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	values := []any{event.ID, event.WorkspaceID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary, string(metadata), string(before), string(after), event.CreatedAt}
	query := "INSERT INTO " + r.store.TableIdentifier("_audit_events") + " (" + strings.Join(database.QuotedColumns(r.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(r.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert metadata change audit: %w", err)
	}
	return nil
}

func (r MetadataStore) syncLocalizedProjection(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, payload []byte, sourceID, now string) error {
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return fmt.Errorf("decode metadata localized text projection: %w", err)
	}
	rawI18n, declared := document["i18n"]
	if !declared {
		return nil
	}
	workspaceID, entityType, entityKey := principalmodel.InstallationWorkspaceID, strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("business_localized_text")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("entity_type")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("entity_key")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("source_kind")+" = "+r.store.Placeholder(4), workspaceID, entityType, entityKey, "metadata_definition"); err != nil {
		return fmt.Errorf("clear metadata localized text projection: %w", err)
	}
	for _, projection := range metadataLocalizedProjections(rawI18n) {
		update := "UPDATE " + r.store.TableIdentifier("business_localized_text") + " SET " + r.store.Identifier("text") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("source_kind") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("source_id") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("entity_type") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("entity_key") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("property") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("locale") + " = " + r.store.Placeholder(9)
		result, err := tx.ExecContext(ctx, update, projection.Text, "metadata_definition", sourceID, now, workspaceID, entityType, entityKey, projection.Property, projection.Locale)
		if err != nil {
			return fmt.Errorf("update metadata localized text projection: %w", err)
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read updated metadata localized text projection rows: %w", rowsErr)
		}
		if affected == 1 {
			continue
		}
		localized := metadatamodel.LocalizedText{WorkspaceID: workspaceID, EntityType: entityType, EntityKey: entityKey, Property: projection.Property, Locale: projection.Locale, Text: projection.Text}
		columns := []string{"id", "workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}
		values := []any{localizedTextID(localized), workspaceID, entityType, entityKey, projection.Property, projection.Locale, projection.Text, "metadata_definition", sourceID, now, now}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("business_localized_text")+" ("+joinIdentifiers(r.store, columns...)+") VALUES ("+joinPlaceholders(r.store, len(values))+")", values...); err != nil {
			return fmt.Errorf("insert metadata localized text projection: %w", err)
		}
	}
	return nil
}

func (r MetadataStore) currentHash(ctx context.Context, tx *sql.Tx, table, resourceKey string) string {
	var current string
	_ = tx.QueryRowContext(ctx, "SELECT "+r.store.Identifier("schema_hash")+" FROM "+r.store.TableIdentifier(table)+" WHERE "+r.store.Identifier("resource_key")+" = "+r.store.Placeholder(1), resourceKey).Scan(&current)
	return current
}

func (r MetadataStore) RollbackDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("begin rollback: %w", err)
	}
	defer tx.Rollback()
	query := "SELECT " + r.store.Identifier("payload_json") + ", " + r.store.Identifier("schema_hash") + " FROM " + r.store.TableIdentifier("metadata_definition_versions") + " WHERE " + r.store.Identifier("resource_type") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("resource_key") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("schema_version") + " = " + r.store.Placeholder(3)
	var payloadJSON, targetHash string
	if err := tx.QueryRowContext(ctx, query, resourceType, resourceKey, request.TargetVersion).Scan(&payloadJSON, &targetHash); err == sql.ErrNoRows {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("metadata.version.notFound: %s@%s", resourceKey, request.TargetVersion)
	} else if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback read version: %w", err)
	}
	nextVersion, err := r.nextVersion(ctx, tx, resourceType, resourceKey)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	shape, err := metadataDefinitionShape(ctx, resourceType, resourceKey, metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(payloadJSON)})
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback decode target: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	update := "UPDATE " + r.store.TableIdentifier(table) + " SET " + r.store.Identifier("payload_json") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("object_key") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("name") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("schema_version") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("schema_hash") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("source_kind") + " = " + r.store.Placeholder(6) + ", " + r.store.Identifier("source_id") + " = " + r.store.Placeholder(7) + ", " + r.store.Identifier("disabled_at") + " = NULL, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(8) + " WHERE " + r.store.Identifier("resource_key") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("schema_hash") + " = " + r.store.Placeholder(10)
	result, err := tx.ExecContext(ctx, update, payloadJSON, shape.ObjectKey, shape.Name, nextVersion, targetHash, "builder", request.ChangePlanID, now, resourceKey, request.ExpectedSchemaHash)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback update: %w", err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("read rollback update rows: %w", rowsErr)
	}
	if affected != 1 {
		return metadatamodel.MetadataDefinition{}, &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: request.ExpectedSchemaHash, CurrentHash: r.currentHash(ctx, tx, table, resourceKey)}
	}
	if err := r.insertDefinitionVersion(ctx, tx, resourceType, resourceKey, nextVersion, targetHash, []byte(payloadJSON), now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.syncLocalizedProjection(ctx, tx, resourceType, resourceKey, []byte(payloadJSON), request.ChangePlanID, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.insertChangeAudit(ctx, tx, audit); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.stageRollbackIntent(ctx, tx, resourceType, resourceKey, request, audit, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := r.refreshCatalogHashTx(ctx, tx, now); err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("refresh metadata catalog hash during rollback: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("commit rollback: %w", err)
	}
	definition, _, err := r.GetDefinition(ctx, scope, resourceType, resourceKey)
	return definition, err
}

func (r MetadataStore) stageRollbackIntent(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent, now string) error {
	planID := strings.TrimSpace(request.ChangePlanID)
	if planID == "" {
		return fmt.Errorf("rollback change plan id is required")
	}
	workspaceID, err := principalmodel.NewWorkspaceID(audit.WorkspaceID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"operation": "metadata_rollback", "resource_type": resourceType, "resource_key": resourceKey, "target_version": request.TargetVersion, "expected_schema_hash": request.ExpectedSchemaHash, "business_reason": request.BusinessReason, "builder_task_id": request.BuilderTaskID})
	update := "UPDATE " + r.store.TableIdentifier("business_change_plan_drafts") + " SET " + r.store.Identifier("status") + " = 'applying', " + r.store.Identifier("payload_json") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_by") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("revision") + " = " + r.store.Identifier("revision") + " + 1 WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = 'draft'"
	result, err := tx.ExecContext(ctx, update, string(payload), audit.ActorID, now, workspaceID.String(), planID)
	if err != nil {
		return fmt.Errorf("stage rollback change plan: %w", err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("read staged rollback change plan rows: %w", rowsErr)
	}
	if affected == 1 {
		return nil
	}
	columns := []string{"workspace_id", "plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at"}
	values := []any{workspaceID.String(), planID, 1, "applying", string(payload), audit.ActorID, audit.ActorID, now, now}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("business_change_plan_drafts")+" ("+joinIdentifiers(r.store, columns...)+") VALUES ("+joinPlaceholders(r.store, len(values))+")", values...); err != nil {
		return fmt.Errorf("create rollback change plan intent: %w", err)
	}
	return nil
}
