// Workflow definition persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type WorkflowDefinitionStore struct {
	store *database.RuntimeStore
	db    workflowDatabase
}

func NewWorkflowDefinitionStore(store *database.RuntimeStore) WorkflowDefinitionStore {
	return WorkflowDefinitionStore{store: store}
}

func (r WorkflowDefinitionStore) database() workflowDatabase {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

func (r WorkflowDefinitionStore) InsertDefinition(ctx context.Context, definition workflowmodel.WorkflowDefinition, draft workflowmodel.WorkflowDefinitionVersion) error {
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, r.store.InsertStatement("workflow_definition_identities", workflowDefinitionColumns()), definition.ID, definition.Key, definition.Name, database.NullableText(definition.OwnerUserID), database.BoolInt(definition.Enabled), database.NullableText(draft.ID), nil, definition.CreatedAt, definition.UpdatedAt); err != nil {
		return err
	}
	if err = r.insertVersion(ctx, tx, draft, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (r WorkflowDefinitionStore) GetDefinitionByKey(ctx context.Context, key string) (workflowmodel.WorkflowDefinition, bool, error) {
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowDefinitionColumns()), ", ")+" FROM "+r.store.TableIdentifier("workflow_definition_identities")+" WHERE "+r.store.Identifier("workflow_key")+" = "+r.store.Placeholder(1), strings.TrimSpace(key))
	definition, err := scanWorkflowDefinition(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowDefinition{}, false, nil
	}
	return definition, err == nil, err
}

func (r WorkflowDefinitionStore) ListDefinitions(ctx context.Context) ([]workflowmodel.WorkflowDefinition, error) {
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowDefinitionColumns()), ", ")+" FROM "+r.store.TableIdentifier("workflow_definition_identities")+" ORDER BY "+r.store.Identifier("workflow_key"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowDefinition{}
	for rows.Next() {
		value, err := scanWorkflowDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (r WorkflowDefinitionStore) GetVersion(ctx context.Context, versionID string) (workflowmodel.WorkflowDefinitionVersion, bool, error) {
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowDefinitionVersionColumns()), ", ")+" FROM "+r.store.TableIdentifier("workflow_definition_versions")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1), versionID)
	version, err := scanWorkflowDefinitionVersion(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowDefinitionVersion{}, false, nil
	}
	return version, err == nil, err
}

func (r WorkflowDefinitionStore) ListVersions(ctx context.Context, definitionID string) ([]workflowmodel.WorkflowDefinitionVersion, error) {
	query := "SELECT " + strings.Join(database.QuotedColumns(r.store, workflowDefinitionVersionColumns()), ", ") + " FROM " + r.store.TableIdentifier("workflow_definition_versions")
	args := []any{}
	if definitionID = strings.TrimSpace(definitionID); definitionID != "" {
		query += " WHERE " + r.store.Identifier("definition_id") + " = " + r.store.Placeholder(1)
		args = append(args, definitionID)
	}
	query += " ORDER BY " + r.store.Identifier("version_no") + " DESC"
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowDefinitionVersion{}
	for rows.Next() {
		value, err := scanWorkflowDefinitionVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (r WorkflowDefinitionStore) UpdateDraft(ctx context.Context, version workflowmodel.WorkflowDefinitionVersion, expectedRevision int) (bool, error) {
	workflowJSON, _ := json.Marshal(version.Workflow)
	reportJSON, _ := json.Marshal(version.ValidationReport)
	query := "UPDATE " + r.store.TableIdentifier("workflow_definition_versions") + " SET " + r.store.Identifier("workflow_json") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("validation_report_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("revision") + " = " + r.store.Identifier("revision") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(6)
	result, err := r.database().ExecContext(ctx, query, string(workflowJSON), string(reportJSON), version.UpdatedAt, version.ID, workflowmodel.WorkflowVersionDraft, expectedRevision)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r WorkflowDefinitionStore) InsertDraftVersion(ctx context.Context, definitionID string, draft workflowmodel.WorkflowDefinitionVersion) (bool, error) {
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_definition_identities")+" SET "+r.store.Identifier("current_draft_version_id")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("current_draft_version_id")+" IS NULL", draft.ID, draft.UpdatedAt, definitionID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	if err = r.insertVersion(ctx, tx, draft, ""); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r WorkflowDefinitionStore) DeleteDraft(ctx context.Context, definitionID, versionID string) (bool, error) {
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("workflow_definition_versions")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("definition_id")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("status")+" = "+r.store.Placeholder(3), versionID, definitionID, workflowmodel.WorkflowVersionDraft)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_definition_identities")+" SET "+r.store.Identifier("current_draft_version_id")+" = NULL WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("current_draft_version_id")+" = "+r.store.Placeholder(2), definitionID, versionID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r WorkflowDefinitionStore) PublishDraft(ctx context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion, idempotencyKey string) (bool, error) {
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	report, _ := json.Marshal(version.ValidationReport)
	workflowJSON, _ := json.Marshal(version.Workflow)
	query := "UPDATE " + r.store.TableIdentifier("workflow_definition_versions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("content_hash") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("validation_report_json") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("publish_note") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("published_by") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("publish_idempotency_key") + " = " + r.store.Placeholder(6) + ", " + r.store.Identifier("published_at") + " = " + r.store.Placeholder(7) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(8) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(11) + " AND " + r.store.Identifier("workflow_json") + " = " + r.store.Placeholder(12)
	result, err := tx.ExecContext(ctx, query, workflowmodel.WorkflowVersionPublished, version.ContentHash, string(report), version.PublishNote, version.PublishedBy, idempotencyKey, version.PublishedAt, version.UpdatedAt, version.ID, workflowmodel.WorkflowVersionDraft, version.Revision, string(workflowJSON))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_definition_identities")+" SET "+r.store.Identifier("current_draft_version_id")+" = NULL, "+r.store.Identifier("current_published_version_id")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(3), version.ID, definition.UpdatedAt, definition.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r WorkflowDefinitionStore) ArchiveVersion(ctx context.Context, definitionID, versionID, archivedAt string) (bool, error) {
	query := "UPDATE " + r.store.TableIdentifier("workflow_definition_versions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("archived_at") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("definition_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("id") + " NOT IN (SELECT " + r.store.Identifier("current_published_version_id") + " FROM " + r.store.TableIdentifier("workflow_definition_identities") + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(7) + ")"
	result, err := r.database().ExecContext(ctx, query, workflowmodel.WorkflowVersionArchived, archivedAt, archivedAt, versionID, definitionID, workflowmodel.WorkflowVersionPublished, definitionID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r WorkflowDefinitionStore) SetDefinitionEnabled(ctx context.Context, definitionID string, enabled bool, updatedAt string) (bool, error) {
	result, err := r.database().ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_definition_identities")+" SET "+r.store.Identifier("enabled")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(3), database.BoolInt(enabled), updatedAt, definitionID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r WorkflowDefinitionStore) insertVersion(ctx context.Context, tx *sql.Tx, version workflowmodel.WorkflowDefinitionVersion, idempotencyKey string) error {
	workflowJSON, _ := json.Marshal(version.Workflow)
	reportJSON, _ := json.Marshal(version.ValidationReport)
	values := []any{version.ID, version.DefinitionID, version.Version, version.Status, version.Revision, database.NullableText(version.ContentHash), string(workflowJSON), string(reportJSON), database.NullableText(version.PublishNote), version.CreatedBy, database.NullableText(version.PublishedBy), database.NullableText(idempotencyKey), version.CreatedAt, version.UpdatedAt, database.NullableText(version.PublishedAt), database.NullableText(version.ArchivedAt)}
	_, err := tx.ExecContext(ctx, r.store.InsertStatement("workflow_definition_versions", workflowDefinitionVersionColumns()), values...)
	return err
}
