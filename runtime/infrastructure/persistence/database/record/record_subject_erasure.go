package record

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

func (r RecordStore) ApplySubjectErasure(ctx context.Context, workspaceID string, objects []definitionmodel.ObjectSchema, mutations []recordmodel.SubjectErasureMutation) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	if actionExecutionTransaction(ctx) != nil {
		return fmt.Errorf("subject erasure must execute outside a business action transaction")
	}
	byKey := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		byKey[object.Key] = object
	}
	tx, err := r.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, mutation := range mutations {
		object, found := byKey[mutation.ObjectKey]
		if !found || strings.TrimSpace(mutation.RecordID) == "" || mutation.AfterUpdatedAt == "" || mutation.AfterUpdatedAt == mutation.BeforeUpdatedAt {
			return fmt.Errorf("invalid prepared subject record mutation")
		}
		statement, args, err := query.NewWorkspaceSelectBuilder(r.store.RuntimeRenderer(), object.Key, workspaceID).Columns("updated_at").Where(query.Equal("id", mutation.RecordID)).Build()
		if err != nil {
			return err
		}
		var updatedAt string
		if err := tx.QueryRowContext(ctx, statement, args...).Scan(&updatedAt); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		if updatedAt == mutation.AfterUpdatedAt {
			continue
		}
		if updatedAt != mutation.BeforeUpdatedAt {
			return fmt.Errorf("subject record %s/%s changed after erasure planning", object.Key, mutation.RecordID)
		}
		fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
		for _, field := range object.Fields {
			fields[field.Key] = field
		}
		keys := make([]string, 0, len(mutation.Values))
		for key := range mutation.Values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder := query.NewWorkspaceUpdateBuilder(r.store.RuntimeRenderer(), object.Key, workspaceID).Set("updated_at", mutation.AfterUpdatedAt)
		for _, key := range keys {
			field, found := fields[key]
			if !found || recordFieldIsSystemOwned(key) || recordpolicy.RecordSubjectEraseMode(field) == recordpolicy.RecordLifecycleEraseRetain {
				return fmt.Errorf("subject erasure field is not declared: %s.%s", object.Key, key)
			}
			builder.Set(key, dbFieldValue(r.store.RuntimeEngine, field, mutation.Values[key]))
			// Localized display values are another persisted copy of this field.
			// Remove every translation in the same transaction; an anonymized
			// base value remains the fallback, while retained fields are untouched.
			if recordmodel.RecordFieldIsLocalized(field) {
				statement, args, err := query.NewWorkspaceDeleteBuilder(r.store.RuntimeRenderer(), recordLocalizedValueTable, workspaceID).
					Where(query.And(query.Equal("object_key", object.Key), query.Equal("record_id", mutation.RecordID), query.Equal("field_key", key))).Build()
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
					return err
				}
			}
		}
		statement, args, err = builder.Where(query.And(query.Equal("id", mutation.RecordID), query.Equal("updated_at", mutation.BeforeUpdatedAt))).Build()
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("subject record changed during erasure")
		}
	}
	return tx.Commit()
}
