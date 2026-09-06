package record

import (
	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

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
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"

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

	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func (r RecordStore) applyRecordMutationTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	s := r.store
	operation := strings.TrimSpace(commit.Operation)
	if commit.AuthorizationScope != nil && (operation == "update" || operation == "restore" || operation == "delete") {
		recordID := strings.TrimSpace(commit.RecordID)
		if recordID == "" {
			recordID = strings.TrimSpace(commit.Record.ID)
		}
		allowed, inspectErr := recordMutationScopeAllowedTx(ctx, tx, s, workspaceID, commit.Object, recordID, commit.AuthorizationScope)
		if inspectErr != nil {
			return inspectErr
		}
		if !allowed {
			return mutation.PolicyConflict("backend.record.outside_scope", commit.Object.Key, recordID, "authorization_scope")
		}
	}
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
		queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Columns(columns...).Values(values...).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, queryValue, args...); err != nil {
			return fmt.Errorf("insert record mutation: %w", database.MutationConstraintError(err, commit.Object.Key, commit.Record.ID, mutation.MutationConflictUnique))
		}
	case "update", "restore":
		builder := query.NewWorkspaceUpdateBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Set("updated_at", commit.Record.UpdatedAt)
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
		predicates := []query.Predicate{query.Equal("id", commit.Record.ID)}
		if commit.AuthorizationScope != nil {
			authorizationPredicate, err := querypersistence.BuildTenantPredicate(s, workspaceID, recordmodel.RecordListQuery{
				AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: commit.Object.Key, ScopeExpression: commit.AuthorizationScope,
			})
			if err != nil {
				return fmt.Errorf("compile record mutation authorization scope: %w", err)
			}
			predicates = append(predicates, authorizationPredicate)
		}
		conditionKeys := make([]string, 0, len(commit.Conditions))
		for key := range commit.Conditions {
			if strings.TrimSpace(key) != "" && key != "id" && key != "updated_at" {
				conditionKeys = append(conditionKeys, key)
			}
		}
		sort.Strings(conditionKeys)
		for _, key := range conditionKeys {
			predicates = append(predicates, query.Equal(key, recordConditionDBValue(s.RuntimeEngine, commit.Object, key, commit.Conditions[key])))
		}
		for _, mutationPredicate := range commit.Predicates {
			predicate, err := recordMutationPredicate(commit.Object, mutationPredicate, s.RuntimeEngine)
			if err != nil {
				return err
			}
			predicates = append(predicates, predicate)
		}
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			predicates = append(predicates, query.Equal("updated_at", expected))
		}
		queryValue, args, buildErr := builder.Where(query.And(predicates...)).Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, queryValue, args...)
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
				lookup, lookupArgs, buildErr := query.NewWorkspaceSelectBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Columns("updated_at").Where(query.Equal("id", commit.Record.ID)).Limit(1).Build()
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
	case "conditional_update_many":
		if err := r.applyConditionalUpdateManyTx(ctx, tx, workspaceID, commit); err != nil {
			return err
		}
	case "delete":
		id := strings.TrimSpace(commit.RecordID)
		if id == "" {
			id = commit.Record.ID
		}
		predicates := []query.Predicate{query.Equal("id", id)}
		if commit.AuthorizationScope != nil {
			authorizationPredicate, err := querypersistence.BuildTenantPredicate(s, workspaceID, recordmodel.RecordListQuery{
				AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: commit.Object.Key, ScopeExpression: commit.AuthorizationScope,
			})
			if err != nil {
				return fmt.Errorf("compile record delete authorization scope: %w", err)
			}
			predicates = append(predicates, authorizationPredicate)
		}
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			predicates = append(predicates, query.Equal("updated_at", expected))
		}
		queryValue, args, buildErr := query.NewWorkspaceDeleteBuilder(s.SQLRenderer, commit.Object.Key, workspaceID).Where(query.And(predicates...)).Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, queryValue, args...)
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
		if err := r.insertPublicationHandoffTx(ctx, tx, message); err != nil {
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

func (r RecordStore) applyConditionalUpdateManyTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	if commit.SetExpectedAffected < 1 || commit.SetExpectedAffected > 200 || len(commit.SetRecordIDs) != commit.SetExpectedAffected || len(commit.Record.Data) == 0 || commit.SetFilterExpression == nil || len(commit.SetExactCoverageValues) != commit.SetExpectedAffected {
		return fmt.Errorf("conditional update-many commit is invalid")
	}
	ids := make([]any, 0, len(commit.SetRecordIDs))
	seen := map[string]bool{}
	for _, raw := range commit.SetRecordIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			return fmt.Errorf("conditional update-many record set is invalid")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	filter, err := recordvalidation.RecordNormalizeFilterExpression(commit.Object, commit.SetFilterExpression)
	if err != nil {
		return fmt.Errorf("normalize conditional update-many filter: %w", err)
	}
	authorizationMode := recordmodel.RecordQueryAuthorizationUnrestricted
	if commit.AuthorizationScope != nil {
		authorizationMode = recordmodel.RecordQueryAuthorizationPredicate
	}
	queryValue := recordQueryDBValues(r.store.RuntimeEngine, commit.Object, recordmodel.RecordListQuery{
		AuthorizationMode: authorizationMode,
		RootObjectKey:     commit.Object.Key, ScopeExpression: commit.AuthorizationScope,
		FilterExpression: filter, OwnerOrganizationScopeID: strings.TrimSpace(commit.SetOwnerOrganizationScope),
	})
	predicate, err := recordLocalizedSearchPredicate(r.store, workspaceID, commit.Object, queryValue)
	if err != nil {
		return fmt.Errorf("compile conditional update-many scope: %w", err)
	}
	predicate = query.And(predicate, query.In("id", ids...))
	builder := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, commit.Object.Key, workspaceID).
		Set("updated_at", commit.Record.UpdatedAt)
	if userID := strings.TrimSpace(commit.Record.UpdateBy); userID != "" {
		builder.Set("update_by", userID)
	}
	fields := make([]string, 0, len(commit.Record.Data))
	fieldCatalog := map[string]definitionmodel.FieldSchema{"id": {Key: "id", Type: "text"}}
	for _, field := range commit.Object.Fields {
		fieldCatalog[field.Key] = field
	}
	coverageFieldKey, coverageValues, err := r.conditionalUpdateManyCoverageDBValues(commit)
	if err != nil {
		return err
	}
	for key := range commit.Record.Data {
		if _, ok := fieldCatalog[key]; !ok || recordFieldIsSystemOwned(key) {
			return fmt.Errorf("conditional update-many field %q is invalid", key)
		}
		fields = append(fields, key)
	}
	sort.Strings(fields)
	for _, key := range fields {
		builder.Set(key, dbFieldValue(r.store.RuntimeEngine, fieldCatalog[key], commit.Record.Data[key]))
	}
	predicate = query.And(predicate, query.In(coverageFieldKey, coverageValues...))
	statement, args, err := builder.Where(predicate).Build()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return fmt.Errorf("conditional update-many mutation: %w", database.MutationConstraintError(err, commit.Object.Key, commit.Record.ID, mutation.MutationConflictUnique))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect conditional update-many mutation: %w", err)
	}
	if affected != int64(commit.SetExpectedAffected) {
		return mutation.PolicyConflict("backend.action.conditional_update_many_affected_mismatch", commit.Object.Key, commit.Record.ID, "affected_count")
	}
	return nil
}

func recordMutationScopeAllowedTx(ctx context.Context, tx TransactionExecutor, store *database.RuntimeStore, workspaceID string, object definitionmodel.ObjectSchema, recordID string, scope *recordmodel.RecordScopeExpression) (bool, error) {
	predicate, err := querypersistence.BuildTenantPredicate(store, workspaceID, recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: object.Key, ScopeExpression: scope,
	})
	if err != nil {
		return false, fmt.Errorf("compile record mutation scope inspection: %w", err)
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(store.SQLRenderer, object.Key, workspaceID).
		Columns("id").
		Where(query.And(query.Equal("id", recordID), predicate)).
		Limit(1).
		Build()
	if err != nil {
		return false, err
	}
	var found string
	err = tx.QueryRowContext(ctx, statement, args...).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect record mutation authorization scope: %w", err)
	}
	return strings.TrimSpace(found) != "", nil
}
