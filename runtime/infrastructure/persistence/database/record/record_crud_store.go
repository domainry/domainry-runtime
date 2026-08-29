package record

import (
	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"database/sql"
	"fmt"

	"sort"
	"strings"
)

func (r RecordStore) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	s := r.store
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, object.Key, workspaceID).Projections(ormbuilder.Project(ormbuilder.Star())).Where(ormbuilder.Equal("id", recordID)).Limit(1).Build()
	if buildErr != nil {
		return recordmodel.Record{}, false, buildErr
	}
	rows, err := r.queryExecutor(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return recordmodel.Record{}, false, fmt.Errorf("get record: %w", err)
	}
	defer rows.Close()
	records, err := recordsFromRows(s.RuntimeEngine, object, rows)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	if len(records) == 0 {
		return recordmodel.Record{}, false, nil
	}
	return records[0], true, nil
}

func (r RecordStore) InsertRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	s := r.store
	columns := []string{"id", "created_at", "updated_at"}
	values := []any{record.ID, record.CreatedAt, record.UpdatedAt}
	columns, values, err = appendRecordInsertMetadata(columns, values, record)
	if err != nil {
		return err
	}
	for _, field := range object.Fields {
		if recordFieldIsSystemOwned(field.Key) {
			continue
		}
		if value, ok := record.Data[field.Key]; ok {
			columns = append(columns, field.Key)
			values = append(values, dbFieldValue(s.RuntimeEngine, field, value))
		}
	}
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.SQLRenderer, object.Key, workspaceID).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := r.database().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert record: %w", err)
	}
	return nil
}

func (r RecordStore) UpdateRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	s := r.store
	builder := ormbuilder.NewWorkspaceUpdateBuilder(s.SQLRenderer, object.Key, workspaceID).Set("updated_at", record.UpdatedAt)
	if err := applyRecordUpdateBuilder(builder, record, record.Deleted); err != nil {
		return err
	}
	for _, field := range object.Fields {
		if recordFieldIsSystemOwned(field.Key) {
			continue
		}
		if value, ok := record.Data[field.Key]; ok {
			builder.Set(field.Key, dbFieldValue(s.RuntimeEngine, field, value))
		}
	}
	query, args, buildErr := builder.Where(ormbuilder.Equal("id", record.ID)).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := r.database().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update record: %w", err)
	}
	return nil
}

func (r RecordStore) UpdateRecordWhere(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	s := r.store
	builder := ormbuilder.NewWorkspaceUpdateBuilder(s.SQLRenderer, object.Key, workspaceID).Set("updated_at", record.UpdatedAt)
	if err := applyRecordUpdateBuilder(builder, record, record.Deleted); err != nil {
		return false, err
	}
	for _, field := range object.Fields {
		if recordFieldIsSystemOwned(field.Key) {
			continue
		}
		if value, ok := record.Data[field.Key]; ok {
			builder.Set(field.Key, dbFieldValue(s.RuntimeEngine, field, value))
		}
	}
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("id", record.ID)}
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		predicates = append(predicates, ormbuilder.Equal(key, recordConditionDBValue(s.RuntimeEngine, object, key, conditions[key])))
	}
	query, args, buildErr := builder.Where(ormbuilder.And(predicates...)).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update record with conditions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect conditional update: %w", err)
	}
	return affected == 1, nil
}

func (r RecordStore) DeleteRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	s := r.store
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return fmt.Errorf("begin record delete: %w", err)
	}
	defer tx.Rollback()
	query, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(s.SQLRenderer, object.Key, workspaceID).Where(ormbuilder.Equal("id", recordID)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect record delete: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	if len(recordmodel.RecordLocalizedFieldKeys(object)) > 0 {
		if err := r.deleteRecordLocalizedValuesTx(ctx, tx, workspaceID, object.Key, recordID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record delete: %w", err)
	}
	return nil
}

func (r RecordStore) UniqueExists(ctx context.Context, workspaceID, objectKey, fieldKey, currentID string, value any) (bool, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	s := r.store
	predicate := ormbuilder.Predicate(ormbuilder.Equal(fieldKey, dbValue(value)))
	if strings.TrimSpace(currentID) != "" {
		predicate = ormbuilder.And(predicate, ormbuilder.NotEqual("id", currentID))
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, objectKey, workspaceID).Columns("id").Where(predicate).Limit(1).Build()
	if buildErr != nil {
		return false, buildErr
	}
	var id string
	err = r.queryExecutor(ctx).QueryRowContext(ctx, query, args...).Scan(&id)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}
