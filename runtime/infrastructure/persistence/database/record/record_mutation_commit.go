package record

import (
	ormbuilder "github.com/domainry/domainry-orm/query"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"github.com/domainry/domainry-foundation/mutation"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"
	"database/sql"
	"errors"
	"fmt"

	"sort"
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

func (r RecordStore) CommitRecordMutation(ctx context.Context, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	return r.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{commit})
}

func (r RecordStore) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		first := commits[0]
		return fmt.Errorf("begin record mutation: %w", database.MutationTransactionError(err, first.Object.Key, first.Record.ID))
	}
	defer tx.Rollback()
	for _, commit := range commits {
		if err := r.applyRecordMutationTx(ctx, tx, workspaceID, commit); err != nil {
			return database.MutationTransactionError(err, commit.Object.Key, commit.Record.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		first := commits[0]
		return fmt.Errorf("commit record mutation batch: %w", database.MutationTransactionError(err, first.Object.Key, first.Record.ID))
	}
	if !transactioncontract.ActiveTransaction(ctx) {
		r.PublishCommittedOutboxWakeups(ctx, workspaceID, commits)
		r.PublishCommittedNotificationWakeups(ctx, workspaceID, commits)
	}
	return nil
}

func recordMutationTxOptions() *sql.TxOptions {
	// Record UoWs may span multiple aggregate tables (record, audit, outbox,
	// workflow intent). Serializable plus explicit optimistic predicates gives
	// SQLite, MySQL, and Postgres one fail-closed conflict contract.
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func (r RecordStore) applyRecordMutationTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	s := r.store
	operation := strings.TrimSpace(commit.Operation)
	if operation == "create" || operation == "update" || operation == "restore" {
		if err := r.validateRelatedAggregateInvariantsTx(ctx, tx, workspaceID, commit, operation); err != nil {
			return err
		}
		if err := r.validateTemporalExclusionTx(ctx, tx, workspaceID, commit, operation); err != nil {
			return err
		}
	}
	switch operation {
	case "create":
		columns := []string{"id", "created_at", "updated_at"}
		values := []any{commit.Record.ID, commit.Record.CreatedAt, commit.Record.UpdatedAt}
		columns, values, metadataErr := appendRecordInsertMetadata(columns, values, commit.Record)
		if metadataErr != nil {
			return metadataErr
		}
		for _, field := range commit.Object.Fields {
			if recordFieldIsSystemOwned(field.Key) {
				continue
			}
			if value, ok := commit.Record.Data[field.Key]; ok {
				columns = append(columns, field.Key)
				values = append(values, dbFieldValue(s.RuntimeEngine, field, value))
			}
		}
		query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Columns(columns...).Values(values...).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("insert record mutation: %w", database.MutationConstraintError(err, commit.Object.Key, commit.Record.ID, mutation.MutationConflictUnique))
		}
	case "update", "restore":
		builder := ormbuilder.NewWorkspaceUpdateBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Set("updated_at", commit.Record.UpdatedAt)
		if metadataErr := applyRecordUpdateBuilder(builder, commit.Record, operation == "restore" || commit.Record.Deleted); metadataErr != nil {
			return metadataErr
		}
		for _, field := range commit.Object.Fields {
			if recordFieldIsSystemOwned(field.Key) {
				continue
			}
			if value, ok := commit.Record.Data[field.Key]; ok {
				builder.Set(field.Key, dbFieldValue(s.RuntimeEngine, field, value))
			}
		}
		predicates := []ormbuilder.Predicate{ormbuilder.Equal("id", commit.Record.ID)}
		conditionKeys := make([]string, 0, len(commit.Conditions))
		for key := range commit.Conditions {
			if strings.TrimSpace(key) != "" && key != "id" && key != "updated_at" {
				conditionKeys = append(conditionKeys, key)
			}
		}
		sort.Strings(conditionKeys)
		for _, key := range conditionKeys {
			predicates = append(predicates, ormbuilder.Equal(key, recordConditionDBValue(s.RuntimeEngine, commit.Object, key, commit.Conditions[key])))
		}
		for _, mutationPredicate := range commit.Predicates {
			predicate, err := recordMutationPredicate(commit.Object, mutationPredicate, s.RuntimeEngine)
			if err != nil {
				return err
			}
			predicates = append(predicates, predicate)
		}
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			predicates = append(predicates, ormbuilder.Equal("updated_at", expected))
		}
		query, args, buildErr := builder.Where(ormbuilder.And(predicates...)).Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update record mutation: %w", database.MutationConstraintError(err, commit.Object.Key, commit.Record.ID, mutation.MutationConflictUnique))
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect record mutation update: %w", err)
		}
		if affected == 0 {
			if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
				var current string
				lookup, lookupArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Columns("updated_at").Where(ormbuilder.Equal("id", commit.Record.ID)).Limit(1).Build()
				if buildErr != nil {
					return buildErr
				}
				err := tx.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&current)
				if errors.Is(err, sql.ErrNoRows) || (err == nil && strings.TrimSpace(current) != expected) {
					return mutation.MutationConflict(commit.Object.Key, commit.Record.ID, mutation.MutationConflictOptimistic, nil)
				}
				if err != nil {
					return fmt.Errorf("inspect record mutation optimistic conflict: %w", err)
				}
			}
			if len(commit.Predicates) > 0 {
				predicate := commit.Predicates[0]
				return mutation.PolicyConflict(predicate.ErrorCode, commit.Object.Key, commit.Record.ID, predicate.Field)
			}
			return mutation.MutationConflict(commit.Object.Key, commit.Record.ID, mutation.MutationConflictOptimistic, nil)
		}
	case "delete":
		id := strings.TrimSpace(commit.RecordID)
		if id == "" {
			id = commit.Record.ID
		}
		predicate := ormbuilder.Predicate(ormbuilder.Equal("id", id))
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			predicate = ormbuilder.And(predicate, ormbuilder.Equal("updated_at", expected))
		}
		query, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Where(predicate).Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("delete record mutation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect record mutation delete: %w", err)
		}
		if affected == 0 {
			return mutation.MutationConflict(commit.Object.Key, id, mutation.MutationConflictOptimistic, nil)
		}
		if len(recordmodel.RecordLocalizedFieldKeys(commit.Object)) > 0 {
			if err := r.deleteRecordLocalizedValuesTx(ctx, tx, workspaceID, commit.Object.Key, id); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported record mutation operation %q", commit.Operation)
	}
	if operation != "delete" {
		if err := r.applyRecordLocalizedMutationsTx(ctx, tx, workspaceID, commit.Object, commit.Record.ID, commit.Record.UpdatedAt, commit.LocalizedValues); err != nil {
			return err
		}
	}
	if commit.Audit != nil {
		audit := *commit.Audit
		audit.WorkspaceID = workspaceID
		if err := r.insertAuditEventTx(ctx, tx, audit); err != nil {
			return err
		}
	}
	for _, audit := range commit.Audits {
		audit.WorkspaceID = workspaceID
		if err := r.insertAuditEventTx(ctx, tx, audit); err != nil {
			return err
		}
	}
	for _, message := range commit.Outbox {
		message.WorkspaceID = workspaceID
		if err := r.insertIntegrationOutboxTx(ctx, tx, message); err != nil {
			return err
		}
	}
	for _, intent := range commit.WorkflowIntents {
		intent.WorkspaceID = workspaceID
		if err := r.insertWorkflowIntentTx(ctx, tx, intent); err != nil {
			return err
		}
	}
	for _, event := range commit.NotificationEvents {
		event.WorkspaceID = workspaceID
		if err := notificationpersistence.NewInboxEventWriter(r.store).InsertEventTx(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}
