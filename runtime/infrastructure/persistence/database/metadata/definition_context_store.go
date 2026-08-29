package metadata

import auditmodel "github.com/domainry/domainry-audit-sdk/contract"
import auditmoduleimpl "github.com/domainry/domainry-audit/module"
import auditsdk "github.com/domainry/domainry-audit-sdk"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	transactionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/transaction"
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
	query, args, buildErr := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, table).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("build %s %s insert: %w", resourceType, shape.Key, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
	if err := r.insertDefinitionRefreshIntentTx(ctx, tx, definition); err != nil {
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

func (r MetadataStore) insertDefinitionRefreshIntentTx(ctx context.Context, tx *sql.Tx, definition metadatamodel.MetadataDefinition) error {
	_, err := transactionpersistence.NewBoundaryIntentStore(r.store).InsertBoundaryIntentTx(ctx, tx, transactionmodel.BoundaryIntent{
		ID:             metadataDefinitionRefreshIntentID(definition.ResourceType, definition.ResourceKey, definition.SchemaHash),
		WorkspaceID:    principalmodel.InstallationWorkspaceID,
		Owner:          "metadata",
		Operation:      "runtime_refresh",
		ResourceID:     definition.ResourceType + ":" + definition.ResourceKey,
		IdempotencyKey: definition.SchemaHash,
		Status:         transactionmodel.BoundaryIntentExecuting,
		Payload:        map[string]any{"resource_type": definition.ResourceType, "resource_key": definition.ResourceKey, "schema_version": definition.SchemaVersion, "schema_hash": definition.SchemaHash},
		LeaseOwner:     "metadata-inline",
		LeaseExpiresAt: time.Now().UTC().Add(90 * time.Second).Format(time.RFC3339Nano),
		FencingToken:   1,
	})
	if err != nil {
		return fmt.Errorf("insert metadata refresh intent: %w", err)
	}
	return nil
}

func (r MetadataStore) CompleteDefinitionRefresh(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, schemaHash, errorText string) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	id := metadataDefinitionRefreshIntentID(strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey), strings.TrimSpace(schemaHash))
	status, nextAttemptAt := transactionmodel.BoundaryIntentSucceeded, ""
	if strings.TrimSpace(errorText) != "" {
		status = transactionmodel.BoundaryIntentReconciliationRequired
		nextAttemptAt = time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	}
	_, err := transactionpersistence.NewBoundaryIntentStore(r.store).TransitionBoundaryIntent(ctx, principalmodel.InstallationWorkspaceID, id, "metadata-inline", 1, status, errorText, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("complete metadata refresh intent: %w", err)
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
	query, args, buildErr := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, table).Set("disabled_at", now).Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if buildErr != nil {
		return fmt.Errorf("build disable %s %s: %w", resourceType, resourceKey, buildErr)
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	if expectedHash == nil {
		query, args, buildErr := ormbuilder.NewDeleteBuilder(r.store.SQLRenderer, table).Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
		if buildErr != nil {
			return fmt.Errorf("build %s %s replacement: %w", resourceType, resourceKey, buildErr)
		}
		_, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("replace %s %s: %w", resourceType, resourceKey, err)
		}
		return nil
	}
	expected := strings.TrimSpace(*expectedHash)
	if expected == "" {
		var current string
		query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).Columns("schema_hash").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
		if buildErr != nil {
			return fmt.Errorf("build current %s %s version lookup: %w", resourceType, resourceKey, buildErr)
		}
		err := tx.QueryRowContext(ctx, query, args...).Scan(&current)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current %s %s version: %w", resourceType, resourceKey, err)
		}
		return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
	}
	query, args, buildErr := ormbuilder.NewDeleteBuilder(r.store.SQLRenderer, table).Where(ormbuilder.And(ormbuilder.Equal("resource_key", resourceKey), ormbuilder.Equal("schema_hash", expected))).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s expected replacement: %w", resourceType, resourceKey, buildErr)
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	lookup, lookupArgs, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).Columns("schema_hash").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if buildErr != nil {
		return fmt.Errorf("build conflicting %s %s version lookup: %w", resourceType, resourceKey, buildErr)
	}
	if err := tx.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&current); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read conflicting %s %s version: %w", resourceType, resourceKey, err)
	}
	return &metadatamodel.MetadataDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current}
}

func (r MetadataStore) nextVersion(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string) (string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "metadata_definition_versions").Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.Equal("resource_type", resourceType), ormbuilder.Equal("resource_key", resourceKey))).Build()
	if buildErr != nil {
		return "", fmt.Errorf("build metadata version count: %w", buildErr)
	}
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return "", fmt.Errorf("read metadata version count: %w", err)
	}
	return fmt.Sprintf("%d", count+1), nil
}

func (r MetadataStore) insertDefinitionVersion(ctx context.Context, tx *sql.Tx, resourceType, resourceKey, version, hash string, payload []byte, now string) error {
	columns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	values := []any{metadataResourceID(resourceType+":version", resourceKey+":"+version+":"+metadataHashPrefix(hash)), resourceType, resourceKey, version, hash, string(payload), now}
	query, args, buildErr := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "metadata_definition_versions").Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s version insert: %w", resourceType, resourceKey, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
		query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID.String()).Set("status", "applying").Set("updated_by", publication.UpdatedBy).Set("updated_at", publication.UpdatedAt).SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Where(ormbuilder.And(ormbuilder.Equal("plan_id", publication.PlanID), ormbuilder.Equal("revision", publication.ExpectedRevision), ormbuilder.Equal("status", "approved"))).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build domain change plan publication: %w", buildErr)
		}
		result, updateErr := tx.ExecContext(ctx, query, args...)
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
	query, args, buildErr := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, table).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("build %s %s insert: %w", mutation.ResourceType, shape.Key, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
	query, args, buildErr := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, table).Set("disabled_at", now).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("resource_key", mutation.ResourceKey), ormbuilder.Equal("schema_hash", expected), ormbuilder.IsNull("disabled_at"))).Build()
	if buildErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("build %s %s archive: %w", mutation.ResourceType, mutation.ResourceKey, buildErr)
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	return auditmoduleimpl.AppendPreparedWithin(ctx, auditsdk.DatabaseHandle{Pool: r.store.DB(), Driver: r.store.Driver(), Schema: r.store.DatabaseSchema()}, runtimeauditmodule.NewTransaction(tx), event)
}

func (r MetadataStore) syncLocalizedProjection(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string, payload []byte, sourceID, now string) error {
	return r.syncMetadataLocalizedTextTx(ctx, tx, resourceType, resourceKey, payload, sourceID, now)
}

func (r MetadataStore) currentHash(ctx context.Context, tx *sql.Tx, table, resourceKey string) string {
	var current string
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).Columns("schema_hash").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if err == nil {
		_ = tx.QueryRowContext(ctx, query, args...).Scan(&current)
	}
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
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "metadata_definition_versions").Columns("payload_json", "schema_hash").Where(ormbuilder.And(ormbuilder.Equal("resource_type", resourceType), ormbuilder.Equal("resource_key", resourceKey), ormbuilder.Equal("schema_version", request.TargetVersion))).Build()
	if buildErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("build rollback version lookup: %w", buildErr)
	}
	var payloadJSON, targetHash string
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&payloadJSON, &targetHash); err == sql.ErrNoRows {
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
	update, updateArgs, buildErr := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, table).Set("payload_json", payloadJSON).Set("object_key", shape.ObjectKey).Set("name", shape.Name).Set("schema_version", nextVersion).Set("schema_hash", targetHash).Set("source_kind", "builder").Set("source_id", request.ChangePlanID).Set("disabled_at", nil).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("resource_key", resourceKey), ormbuilder.Equal("schema_hash", request.ExpectedSchemaHash))).Build()
	if buildErr != nil {
		return metadatamodel.MetadataDefinition{}, fmt.Errorf("build rollback update: %w", buildErr)
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID.String()).Set("status", "applying").Set("payload_json", string(payload)).Set("updated_by", audit.ActorID).Set("updated_at", now).SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Where(ormbuilder.And(ormbuilder.Equal("plan_id", planID), ormbuilder.Equal("status", "draft"))).Build()
	if buildErr != nil {
		return fmt.Errorf("build rollback change plan stage: %w", buildErr)
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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
	insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID.String()).Columns("plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at").Values(planID, 1, "applying", string(payload), audit.ActorID, audit.ActorID, now, now).Build()
	if buildErr != nil {
		return fmt.Errorf("build rollback change plan intent: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
		return fmt.Errorf("create rollback change plan intent: %w", err)
	}
	return nil
}
