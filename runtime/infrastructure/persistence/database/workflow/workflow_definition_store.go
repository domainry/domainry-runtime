// Workflow definition persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
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
	query, args, err := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "workflow_definition_identities").Columns(workflowDefinitionColumns()...).Values(definition.ID, definition.Key, definition.Name, database.NullableText(definition.OwnerUserID), database.BoolInt(definition.Enabled), database.NullableText(draft.ID), nil, definition.CreatedAt, definition.UpdatedAt).Build()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if err = r.insertVersion(ctx, tx, draft, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (r WorkflowDefinitionStore) GetDefinitionByKey(ctx context.Context, key string) (workflowmodel.WorkflowDefinition, bool, error) {
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "workflow_definition_identities").Columns(workflowDefinitionColumns()...).Where(ormbuilder.Equal("workflow_key", strings.TrimSpace(key))).Build()
	if err != nil {
		return workflowmodel.WorkflowDefinition{}, false, err
	}
	row := r.database().QueryRowContext(ctx, query, args...)
	definition, err := scanWorkflowDefinition(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowDefinition{}, false, nil
	}
	return definition, err == nil, err
}

func (r WorkflowDefinitionStore) ListDefinitions(ctx context.Context) ([]workflowmodel.WorkflowDefinition, error) {
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "workflow_definition_identities").Columns(workflowDefinitionColumns()...).OrderBy(ormbuilder.Ascending("workflow_key"), ormbuilder.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "workflow_definition_versions").Columns(workflowDefinitionVersionColumns()...).Where(ormbuilder.Equal("id", versionID)).Build()
	if err != nil {
		return workflowmodel.WorkflowDefinitionVersion{}, false, err
	}
	row := r.database().QueryRowContext(ctx, query, args...)
	version, err := scanWorkflowDefinitionVersion(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowDefinitionVersion{}, false, nil
	}
	return version, err == nil, err
}

func (r WorkflowDefinitionStore) ListVersions(ctx context.Context, definitionID string) ([]workflowmodel.WorkflowDefinitionVersion, error) {
	builder := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "workflow_definition_versions").Columns(workflowDefinitionVersionColumns()...).OrderBy(ormbuilder.Descending("version_no"), ormbuilder.Descending("id"))
	if definitionID = strings.TrimSpace(definitionID); definitionID != "" {
		builder.Where(ormbuilder.Equal("definition_id", definitionID))
	}
	query, args, err := builder.Build()
	if err != nil {
		return nil, err
	}
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
	query, args, err := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_versions").Set("workflow_json", string(workflowJSON)).Set("validation_report_json", string(reportJSON)).SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Set("updated_at", version.UpdatedAt).Where(ormbuilder.And(ormbuilder.Equal("id", version.ID), ormbuilder.Equal("status", workflowmodel.WorkflowVersionDraft), ormbuilder.Equal("revision", expectedRevision))).Build()
	if err != nil {
		return false, err
	}
	result, err := r.database().ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_identities").Set("current_draft_version_id", draft.ID).Set("updated_at", draft.UpdatedAt).Where(ormbuilder.And(ormbuilder.Equal("id", definitionID), ormbuilder.IsNull("current_draft_version_id"))).Build()
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewDeleteBuilder(r.store.SQLRenderer, "workflow_definition_versions").Where(ormbuilder.And(ormbuilder.Equal("id", versionID), ormbuilder.Equal("definition_id", definitionID), ormbuilder.Equal("status", workflowmodel.WorkflowVersionDraft))).Build()
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	query, args, err = ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_identities").Set("current_draft_version_id", nil).Where(ormbuilder.And(ormbuilder.Equal("id", definitionID), ormbuilder.Equal("current_draft_version_id", versionID))).Build()
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
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
	query, args, err := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_versions").Set("status", workflowmodel.WorkflowVersionPublished).Set("content_hash", version.ContentHash).Set("validation_report_json", string(report)).Set("publish_note", version.PublishNote).Set("published_by", version.PublishedBy).Set("publish_idempotency_key", idempotencyKey).Set("published_at", version.PublishedAt).Set("updated_at", version.UpdatedAt).Where(ormbuilder.And(ormbuilder.Equal("id", version.ID), ormbuilder.Equal("status", workflowmodel.WorkflowVersionDraft), ormbuilder.Equal("revision", version.Revision), ormbuilder.Equal("workflow_json", string(workflowJSON)))).Build()
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	query, args, err = ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_identities").Set("current_draft_version_id", nil).Set("current_published_version_id", version.ID).Set("updated_at", definition.UpdatedAt).Where(ormbuilder.Equal("id", definition.ID)).Build()
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r WorkflowDefinitionStore) ArchiveVersion(ctx context.Context, definitionID, versionID, archivedAt string) (bool, error) {
	current := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "workflow_definition_identities").Columns("current_published_version_id").Where(ormbuilder.Equal("id", definitionID))
	query, args, err := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_versions").Set("status", workflowmodel.WorkflowVersionArchived).Set("archived_at", archivedAt).Set("updated_at", archivedAt).Where(ormbuilder.And(ormbuilder.Equal("id", versionID), ormbuilder.Equal("definition_id", definitionID), ormbuilder.Equal("status", workflowmodel.WorkflowVersionPublished), ormbuilder.NotInSubquery("id", current))).Build()
	if err != nil {
		return false, err
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (r WorkflowDefinitionStore) SetDefinitionEnabled(ctx context.Context, definitionID string, enabled bool, updatedAt string) (bool, error) {
	query, args, err := ormbuilder.NewUpdateBuilder(r.store.SQLRenderer, "workflow_definition_identities").Set("enabled", database.BoolInt(enabled)).Set("updated_at", updatedAt).Where(ormbuilder.Equal("id", definitionID)).Build()
	if err != nil {
		return false, err
	}
	result, err := r.database().ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "workflow_definition_versions").Columns(workflowDefinitionVersionColumns()...).Values(values...).Build()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}
