package metadata

import auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s MetadataStore) UpsertMetadataDefinition(ctx context.Context, resourceType string, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinition, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceKey = strings.TrimSpace(resourceKey)
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
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "legacy metadata definition upsert")
	if replay, found, replayErr := s.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr != nil {
		return metadatamodel.MetadataDefinition{}, replayErr
	} else if found {
		return replay, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sourceKind := strings.TrimSpace(req.SourceKind)
	if sourceKind == "" {
		sourceKind = "user"
	}
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = "metadata_api"
	}
	schemaVersion, err := s.nextMetadataSchemaVersion(ctx, resourceType, shape.Key)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("begin metadata upsert: %w", err)
	}
	defer tx.Rollback()
	if err := s.replaceMetadataDefinitionVersion(ctx, tx, table, resourceType, shape.Key, req.ExpectedSchemaHash); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
	values := []any{
		metadataResourceID(resourceType, shape.Key),
		shape.Key,
		shape.ObjectKey,
		shape.Name,
		string(raw),
		schemaVersion,
		hash,
		sourceKind,
		sourceID,
		nil,
		now,
		now,
	}
	insertQuery := "INSERT INTO " + s.store.TableIdentifier(table) + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, insertQuery, values...); err != nil {
		_ = tx.Rollback()
		if replay, found, replayErr := s.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("insert %s %s: %w", resourceType, shape.Key, err)
	}
	versionColumns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	versionValues := []any{
		metadataResourceID(resourceType+":version", shape.Key+":"+schemaVersion+":"+metadataHashPrefix(hash)),
		resourceType,
		shape.Key,
		schemaVersion,
		hash,
		string(raw),
		now,
	}
	versionQuery := "INSERT INTO " + s.store.TableIdentifier("metadata_definition_versions") + " (" + strings.Join(quotedColumns(s.store, versionColumns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(versionColumns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, versionQuery, versionValues...); err != nil {
		_ = tx.Rollback()
		if replay, found, replayErr := s.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("insert %s %s version: %w", resourceType, shape.Key, err)
	}
	if err := tx.Commit(); err != nil {
		if replay, found, replayErr := s.metadataDefinitionReplay(ctx, scope, resourceType, shape.Key, hash, req.ExpectedSchemaHash); replayErr == nil && found {
			return replay, nil
		}
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("commit metadata upsert: %w", err)
	}
	// Refresh catalog schema_hash so /schema ETag changes on the next request.
	return metadatamodel.MetadataDefinition{
		ResourceType:  resourceType,
		ResourceKey:   shape.Key,
		ObjectKey:     shape.ObjectKey,
		Name:          shape.Name,
		Payload:       append([]byte(nil), raw...),
		SchemaVersion: schemaVersion,
		SchemaHash:    hash,
		SourceKind:    sourceKind,
		SourceID:      sourceID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (s MetadataStore) replaceMetadataDefinitionVersion(ctx context.Context, tx *sql.Tx, table, resourceType, resourceKey string, expectedHash *string) error {
	keyColumn, hashColumn := s.store.Identifier("resource_key"), s.store.Identifier("schema_hash")
	if expectedHash == nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+s.store.Placeholder(1), resourceKey); err != nil {
			return fmt.Errorf("replace %s %s: %w", resourceType, resourceKey, err)
		}
		return nil
	}
	expected := strings.TrimSpace(*expectedHash)
	if expected == "" {
		var current string
		err := tx.QueryRowContext(ctx, "SELECT "+hashColumn+" FROM "+s.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+s.store.Placeholder(1), resourceKey).Scan(&current)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current %s %s version: %w", resourceType, resourceKey, err)
		}
		return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+s.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+s.store.Placeholder(1)+" AND "+hashColumn+" = "+s.store.Placeholder(2), resourceKey, expected)
	if err != nil {
		return fmt.Errorf("replace %s %s: %w", resourceType, resourceKey, err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("read replaced %s %s rows: %w", resourceType, resourceKey, rowsErr)
	}
	if affected == 1 {
		return nil
	}
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT "+hashColumn+" FROM "+s.store.TableIdentifier(table)+" WHERE "+keyColumn+" = "+s.store.Placeholder(1), resourceKey).Scan(&current); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read conflicting %s %s version: %w", resourceType, resourceKey, err)
	}
	return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
}

func (s MetadataStore) nextMetadataSchemaVersion(ctx context.Context, resourceType string, resourceKey string) (string, error) {
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("metadata_definition_versions") + " WHERE " + s.store.Identifier("resource_type") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(2)
	var count int
	if err := s.database().QueryRowContext(ctx, query, resourceType, resourceKey).Scan(&count); err != nil {
		return "", fmt.Errorf("read metadata version count: %w", err)
	}
	return fmt.Sprintf("%d", count+1), nil
}

// DisableMetadataDefinition soft-deletes a definition by setting disabled_at; no physical DROP is performed.
func (s MetadataStore) DisableMetadataDefinition(ctx context.Context, resourceType string, resourceKey string) error {
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.database().ExecContext(ctx, "UPDATE "+s.store.TableIdentifier(table)+" SET "+s.store.Identifier("disabled_at")+" = "+s.store.Placeholder(1)+" WHERE "+s.store.Identifier("resource_key")+" = "+s.store.Placeholder(2), now, resourceKey)
	if err != nil {
		return fmt.Errorf("disable %s %s: %w", resourceType, resourceKey, err)
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("read disabled %s %s rows: %w", resourceType, resourceKey, rowsErr)
	}
	if rows == 0 {
		return fmt.Errorf("metadata.%s.notFound: %s", resourceType, resourceKey)
	}
	return nil
}

// ListMetadataDefinitions returns all active (non-disabled) definitions for the given resourceType.
// If workspaceID is non-empty, only definitions matching that workspace are returned (P2-4 isolation).
func (s MetadataStore) ListMetadataDefinitions(ctx context.Context, resourceType string, workspaceID string) ([]metadatamodel.MetadataDefinition, error) {
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	query := "SELECT " + strings.Join(quotedColumns(s.store, []string{"resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}), ", ") +
		" FROM " + s.store.TableIdentifier(table) + " WHERE " + s.store.Identifier("disabled_at") + " IS NULL"
	args := []any{}
	if workspaceID != "" {
		query += " AND " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(len(args)+1)
		args = append(args, workspaceID)
	}
	query += " ORDER BY " + s.store.Identifier("resource_key") + " ASC"
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s definitions: %w", resourceType, err)
	}
	defer rows.Close()
	var out []metadatamodel.MetadataDefinition
	for rows.Next() {
		var d metadatamodel.MetadataDefinition
		var payloadJSON string
		var disabledAt sql.NullString
		if err := rows.Scan(&d.ResourceKey, &d.ObjectKey, &d.Name, &payloadJSON, &d.SchemaVersion, &d.SchemaHash, &d.SourceKind, &d.SourceID, &disabledAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan %s definition: %w", resourceType, err)
		}
		d.ResourceType = resourceType
		d.Payload = json.RawMessage(payloadJSON)
		if disabledAt.Valid {
			d.DisabledAt = disabledAt.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetMetadataDefinition returns a single definition by type and key.
func (s MetadataStore) GetMetadataDefinition(ctx context.Context, resourceType string, resourceKey string) (metadatamodel.MetadataDefinition, bool, error) {
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, false, err
	}
	query := "SELECT " + strings.Join(quotedColumns(s.store, []string{"resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}), ", ") +
		" FROM " + s.store.TableIdentifier(table) + " WHERE " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(1)
	var d metadatamodel.MetadataDefinition
	var payloadJSON string
	var disabledAt sql.NullString
	err = s.database().QueryRowContext(ctx, query, resourceKey).Scan(&d.ResourceKey, &d.ObjectKey, &d.Name, &payloadJSON, &d.SchemaVersion, &d.SchemaHash, &d.SourceKind, &d.SourceID, &disabledAt, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return metadatamodel.MetadataDefinition{}, false, nil
	}
	if err != nil {
		return metadatamodel.MetadataDefinition{}, false, fmt.Errorf("get %s definition: %w", resourceType, err)
	}
	d.ResourceType = resourceType
	d.Payload = json.RawMessage(payloadJSON)
	if disabledAt.Valid {
		d.DisabledAt = disabledAt.String
	}
	return d, true, nil
}

// ListMetadataDefinitionVersions returns the version history for a definition.
func (s MetadataStore) ListMetadataDefinitionVersions(ctx context.Context, resourceType string, resourceKey string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	query := "SELECT " + strings.Join(quotedColumns(s.store, []string{"schema_version", "schema_hash", "payload_json", "created_at"}), ", ") +
		" FROM " + s.store.TableIdentifier("metadata_definition_versions") +
		" WHERE " + s.store.Identifier("resource_type") + " = " + s.store.Placeholder(1) +
		" AND " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(2) +
		" ORDER BY " + s.store.Identifier("created_at") + " DESC"
	rows, err := s.database().QueryContext(ctx, query, resourceType, resourceKey)
	if err != nil {
		return nil, fmt.Errorf("list versions %s %s: %w", resourceType, resourceKey, err)
	}
	defer rows.Close()
	var out []metadatamodel.MetadataDefinitionVersion
	for rows.Next() {
		var v metadatamodel.MetadataDefinitionVersion
		var payloadJSON string
		if err := rows.Scan(&v.SchemaVersion, &v.SchemaHash, &payloadJSON, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		v.ResourceType = resourceType
		v.ResourceKey = resourceKey
		v.Payload = json.RawMessage(payloadJSON)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, leftErr := strconv.Atoi(strings.TrimSpace(out[i].SchemaVersion))
		right, rightErr := strconv.Atoi(strings.TrimSpace(out[j].SchemaVersion))
		if leftErr == nil && rightErr == nil && left != right {
			return left > right
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].SchemaVersion > out[j].SchemaVersion
	})
	return out, nil
}

// RollbackMetadataDefinition restores a definition to a specific schema_version from its version history.
func (s MetadataStore) RollbackMetadataDefinition(ctx context.Context, resourceType string, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("begin rollback: %w", err)
	}
	defer tx.Rollback()
	query := "SELECT " + s.store.Identifier("payload_json") + ", " + s.store.Identifier("schema_hash") + " FROM " + s.store.TableIdentifier("metadata_definition_versions") + " WHERE " + s.store.Identifier("resource_type") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("schema_version") + " = " + s.store.Placeholder(3)
	var payloadJSON, targetHash string
	if err := tx.QueryRowContext(ctx, query, resourceType, resourceKey, request.TargetVersion).Scan(&payloadJSON, &targetHash); err == sql.ErrNoRows {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("metadata.version.notFound: %s@%s", resourceKey, request.TargetVersion)
	} else if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback read version: %w", err)
	}
	nextVersion, err := s.nextMetadataSchemaVersionTx(ctx, tx, resourceType, resourceKey)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	shape, err := metadataDefinitionShape(ctx, resourceType, resourceKey, metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(payloadJSON)})
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback decode target: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updateQuery := "UPDATE " + s.store.TableIdentifier(table) +
		" SET " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("object_key") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("name") + " = " + s.store.Placeholder(3) +
		", " + s.store.Identifier("schema_version") + " = " + s.store.Placeholder(4) +
		", " + s.store.Identifier("schema_hash") + " = " + s.store.Placeholder(5) +
		", " + s.store.Identifier("source_kind") + " = " + s.store.Placeholder(6) + ", " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(7) +
		", " + s.store.Identifier("disabled_at") + " = NULL" +
		", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(8) +
		" WHERE " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(9) + " AND " + s.store.Identifier("schema_hash") + " = " + s.store.Placeholder(10)
	result, err := tx.ExecContext(ctx, updateQuery, payloadJSON, shape.ObjectKey, shape.Name, nextVersion, targetHash, "builder", request.ChangePlanID, now, resourceKey, request.ExpectedSchemaHash)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("rollback update: %w", err)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("read rollback update rows: %w", rowsErr)
	}
	if affected != 1 {
		return metadatamodel.MetadataDefinition{}, &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: request.ExpectedSchemaHash, CurrentHash: s.currentMetadataHashTx(ctx, tx, table, resourceKey)}
	}
	if err := s.insertMetadataDefinitionVersionTx(ctx, tx, resourceType, resourceKey, nextVersion, targetHash, []byte(payloadJSON), now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := s.syncMetadataLocalizedTextTx(ctx, tx, resourceType, resourceKey, []byte(payloadJSON), request.ChangePlanID, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := s.insertMetadataChangeAudit(ctx, tx, audit); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := s.stageMetadataRollbackIntentTx(ctx, tx, resourceType, resourceKey, request, audit, now); err != nil {
		return metadatamodel.MetadataDefinition{}, err
	}
	if err := tx.Commit(); err != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("commit rollback: %w", err)
	}
	d, _, err := s.GetMetadataDefinition(ctx, resourceType, resourceKey)
	return d, err
}

func (s MetadataStore) stageMetadataRollbackIntentTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent, now string) error {
	planID := strings.TrimSpace(request.ChangePlanID)
	if planID == "" {
		return fmt.Errorf("rollback change plan id is required")
	}
	workspaceID, err := principalmodel.NewWorkspaceID(audit.WorkspaceID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"operation": "metadata_rollback", "resource_type": resourceType, "resource_key": resourceKey, "target_version": request.TargetVersion, "expected_schema_hash": request.ExpectedSchemaHash, "business_reason": request.BusinessReason, "builder_task_id": request.BuilderTaskID})
	update := "UPDATE " + s.store.TableIdentifier("business_change_plan_drafts") + " SET " + s.store.Identifier("status") + " = 'applying', " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("updated_by") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("revision") + " = " + s.store.Identifier("revision") + " + 1 WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("plan_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("status") + " = 'draft'"
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.store.TableIdentifier("business_change_plan_drafts")+" ("+joinIdentifiers(s.store, columns...)+") VALUES ("+joinPlaceholders(s.store, len(values))+")", values...); err != nil {
		return fmt.Errorf("create rollback change plan intent: %w", err)
	}
	return nil
}
