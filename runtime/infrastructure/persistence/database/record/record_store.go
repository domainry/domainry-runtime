// Record persistence.
package record

import (
	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/telemetry"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"

	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

// RecordStore is the ctx-first record aggregate view of database.Store. The
// legacy database.Store methods remain during migration, but new services should receive
// this bounded view instead of the full storage object.
type RecordStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewRecordStore(store *database.RuntimeStore) RecordStore {
	return RecordStore{store: store}
}

func (r RecordStore) database() *sql.DB {
	if r.db != nil {
		return r.db
	}
	if r.store == nil {
		return nil
	}
	return r.store.DB()
}

// SchedulerNow returns the database server clock so lease arbitration does not
// depend on clock synchronization between Runtime processes.
func (r RecordStore) SchedulerNow(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	database := r.database()
	if database == nil {
		return time.Time{}, fmt.Errorf("record database is unavailable")
	}
	query := "SELECT CURRENT_TIMESTAMP"
	if r.store != nil {
		switch string(r.store.RuntimeProfile().Name()) {
		case "sqlite":
			query = "SELECT CAST(strftime('%s','now') AS INTEGER)"
		case "mysql":
			query = "SELECT UNIX_TIMESTAMP(UTC_TIMESTAMP(6))"
		case "postgres":
			query = "SELECT EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)"
		}
	}
	var raw any
	if err := database.QueryRowContext(ctx, query).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("read database current timestamp: %w", err)
	}
	switch value := raw.(type) {
	case time.Time:
		return value.UTC(), nil
	case int64:
		return time.Unix(value, 0).UTC(), nil
	case float64:
		seconds, fraction := math.Modf(value)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC(), nil
	case string:
		return parseSchedulerDatabaseTimeOrEpoch(value)
	case []byte:
		return parseSchedulerDatabaseTimeOrEpoch(string(value))
	default:
		return time.Time{}, fmt.Errorf("unsupported database timestamp type %T", raw)
	}
}

func parseSchedulerDatabaseTimeOrEpoch(value string) (time.Time, error) {
	if epoch, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
		seconds, fraction := math.Modf(epoch)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC(), nil
	}
	return parseSchedulerDatabaseTime(value)
}

func parseSchedulerDatabaseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse database current timestamp %q", value)
}

func (r RecordStore) queryExecutor(ctx context.Context) recordQueryExecutor {
	if tx := actionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return r.database()
}

func (r RecordStore) ListDueRecordTimerWorkspaces(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(object.Key) == "" {
		return nil, fmt.Errorf("record workspace object key is required")
	}
	s := r.store
	nowValue := now.UTC().Format(time.RFC3339Nano)
	workspaceColumn := s.Identifier("workspace_id")
	statusColumn := s.Identifier("status")
	dueAtColumn := s.Identifier("due_at")
	leaseExpiresColumn := s.Identifier("lease_expires_at")
	query := "SELECT DISTINCT " + workspaceColumn + " FROM " + s.TableIdentifier(object.Key) +
		" WHERE (" + statusColumn + " = " + s.Placeholder(1) + " AND " + dueAtColumn + " <= " + s.Placeholder(2) + ")" +
		" OR (" + statusColumn + " = " + s.Placeholder(3) + " AND " + dueAtColumn + " <= " + s.Placeholder(4) + " AND " + leaseExpiresColumn + " <= " + s.Placeholder(5) + ")" +
		" ORDER BY " + workspaceColumn + " ASC"
	rows, err := r.database().QueryContext(ctx, query, "scheduled", nowValue, "leased", nowValue, nowValue)
	if err != nil {
		return nil, fmt.Errorf("list record workspaces: %w", err)
	}
	defer rows.Close()
	workspaces := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			workspaces = append(workspaces, workspaceID)
		}
	}
	return workspaces, rows.Err()
}

func (r RecordStore) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	query.FilterExpression, err = recordvalidation.RecordNormalizeFilterExpression(object, query.FilterExpression)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record filter: %w", err)
	}
	query.SelectFields, err = recordvalidation.RecordNormalizeSelectFields(object, query.SelectFields)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("normalize record projection: %w", err)
	}
	lockIntent := strings.TrimSpace(query.LockIntent)
	if lockIntent == "" {
		lockIntent = recordmodel.RecordQueryLockNone
	}
	actionTx := actionExecutionTransaction(ctx)
	switch lockIntent {
	case recordmodel.RecordQueryLockNone:
	case recordmodel.RecordQueryLockForUpdate, recordmodel.RecordQueryLockForUpdateSkipLocked:
		if actionTx == nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("record query lock intent %q requires a transactional claim owner", lockIntent)
		}
	default:
		return recordmodel.RecordPageResult{}, fmt.Errorf("record query lock intent %q is invalid", lockIntent)
	}
	s := r.store
	if query.ScopeDiagnostic != nil {
		return recordmodel.RecordPageResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: query.ScopeDiagnostic.Code, Params: map[string]string{"object_key": query.ScopeDiagnostic.ObjectKey, "detail": query.ScopeDiagnostic.Detail}}
	}
	query = recordQueryDBValues(s.RuntimeEngine, object, query)
	executor := r.queryExecutor(ctx)
	var readTx *sql.Tx
	if query.ScopeExpression != nil && querypersistence.ScopeExpressionHasRelation(*query.ScopeExpression) {
		if actionTx == nil {
			readTx, err = r.database().BeginTx(ctx, recordScopeReadTxOptions(s.RuntimeEngine))
			if err != nil {
				return recordmodel.RecordPageResult{}, fmt.Errorf("begin record scope snapshot: %w", err)
			}
			defer readTx.Rollback()
			executor = readTx
		}
		resolved, resolveErr := querypersistence.ResolveScopeMembership(s, workspaceID, *query.ScopeExpression, querypersistence.ScopeMembershipINThreshold, func(statement string, args ...any) ([]string, error) {
			rows, queryErr := executor.QueryContext(ctx, statement, args...)
			if queryErr != nil {
				return nil, queryErr
			}
			defer rows.Close()
			values := []string{}
			for rows.Next() {
				var value string
				if scanErr := rows.Scan(&value); scanErr != nil {
					return nil, scanErr
				}
				values = append(values, value)
			}
			return values, rows.Err()
		})
		if resolveErr != nil {
			return recordmodel.RecordPageResult{}, resolveErr
		}
		query.ScopeExpression = &resolved
	}
	whereSQL, args, err := recordLocalizedSearchWhere(s, workspaceID, object, query)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	countWhereSQL := whereSQL
	countArgs := append([]any(nil), args...)
	if afterID := strings.TrimSpace(query.AfterID); afterID != "" {
		if !recordQueryUsesAscendingIDOrder(query.Sort) {
			return recordmodel.RecordPageResult{}, fmt.Errorf("record keyset cursor requires ascending id sort")
		}
		args = append(args, afterID)
		whereSQL += " AND " + s.TableIdentifier(object.Key) + "." + s.Identifier("id") + " > " + s.Placeholder(len(args))
	}
	if query.Page > 1 && strings.TrimSpace(query.AfterID) == "" {
		return recordmodel.RecordPageResult{}, fmt.Errorf("record deep pagination requires an id cursor")
	}
	orderSQL, orderArgs := recordLocalizedOrder(s, workspaceID, object, query, len(args))
	var total int
	if !query.SkipTotal {
		if err := executor.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.TableIdentifier(object.Key)+countWhereSQL, countArgs...).Scan(&total); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("count records: %w", err)
		}
	}
	fetchLimit := query.PageSize
	if query.SkipTotal || strings.TrimSpace(query.AfterID) != "" {
		fetchLimit++
	}
	listArgs := append(append([]any{}, args...), orderArgs...)
	listArgs = append(listArgs, fetchLimit)
	lockSQL := recordQueryLockSQL(s.RuntimeEngine, lockIntent)
	rows, err := executor.QueryContext(ctx, "SELECT "+recordListProjection(s, query.SelectFields)+" FROM "+s.TableIdentifier(object.Key)+whereSQL+orderSQL+" LIMIT "+s.Placeholder(len(args)+len(orderArgs)+1)+lockSQL, listArgs...)
	if err != nil {
		return recordmodel.RecordPageResult{}, fmt.Errorf("list records: %w", err)
	}
	records, err := recordsFromRows(s.RuntimeEngine, object, rows)
	if err != nil {
		_ = rows.Close()
		return recordmodel.RecordPageResult{}, err
	}
	_ = rows.Close()
	hasNext := len(records) < total
	if query.SkipTotal || strings.TrimSpace(query.AfterID) != "" {
		hasNext = len(records) > query.PageSize
		if hasNext {
			records = records[:query.PageSize]
		}
	}
	records, err = r.applyRecordLocalization(
		ctx,
		workspaceID,
		object,
		records,
		query.SelectFields,
		query.Locale,
		query.FallbackLocale,
	)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	if readTx != nil {
		if err := readTx.Commit(); err != nil {
			return recordmodel.RecordPageResult{}, fmt.Errorf("commit record scope snapshot: %w", err)
		}
	}
	return recordmodel.RecordPageResult{Items: records, Page: query.Page, PageSize: query.PageSize, Total: total, HasNext: hasNext}, nil
}

func recordQueryUsesAscendingIDOrder(sortRules []recordmodel.RecordSortRule) bool {
	if len(sortRules) == 0 {
		return true
	}
	return len(sortRules) == 1 && strings.TrimSpace(sortRules[0].Field) == "id" && !strings.EqualFold(strings.TrimSpace(sortRules[0].Direction), "desc")
}

type recordQueryExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func recordQueryLockSQL(profile persistencedriver.EngineProfile, intent string) string {
	if !profile.Capabilities().RowLock {
		return ""
	}
	switch intent {
	case recordmodel.RecordQueryLockForUpdate:
		return " FOR UPDATE"
	case recordmodel.RecordQueryLockForUpdateSkipLocked:
		return " FOR UPDATE SKIP LOCKED"
	default:
		return ""
	}
}

func recordScopeReadTxOptions(profile persistencedriver.EngineProfile) *sql.TxOptions {
	return &sql.TxOptions{Isolation: profile.RecordReadIsolation(), ReadOnly: true}
}

func (r RecordStore) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	s := r.store
	query := "SELECT * FROM " + s.TableIdentifier(object.Key) + " WHERE " + s.Identifier("workspace_id") + " = " + s.Placeholder(1) + " AND " + s.Identifier("id") + " = " + s.Placeholder(2) + " LIMIT 1"
	rows, err := r.queryExecutor(ctx).QueryContext(ctx, query, workspaceID, recordID)
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
	columns := []string{"workspace_id", "id", "created_at", "updated_at"}
	values := []any{workspaceID, record.ID, record.CreatedAt, record.UpdatedAt}
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
	query := "INSERT INTO " + s.TableIdentifier(object.Key) + " (" + stringsJoinIdentifiers(s, columns...) + ") VALUES (" + stringsJoinPlaceholders(s, len(columns)) + ")"
	if _, err := r.database().ExecContext(ctx, query, values...); err != nil {
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
	assignments := []string{s.Identifier("updated_at") + " = " + s.Placeholder(1)}
	values := []any{record.UpdatedAt}
	assignments, values, err = appendRecordUpdateMetadata(s, assignments, values, record, record.Deleted)
	if err != nil {
		return err
	}
	for _, field := range object.Fields {
		if recordFieldIsSystemOwned(field.Key) {
			continue
		}
		if value, ok := record.Data[field.Key]; ok {
			assignments = append(assignments, s.Identifier(field.Key)+" = "+s.Placeholder(len(values)+1))
			values = append(values, dbFieldValue(s.RuntimeEngine, field, value))
		}
	}
	values = append(values, workspaceID, record.ID)
	query := "UPDATE " + s.TableIdentifier(object.Key) + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.Identifier("workspace_id") + " = " + s.Placeholder(len(values)-1) + " AND " + s.Identifier("id") + " = " + s.Placeholder(len(values))
	if _, err := r.database().ExecContext(ctx, query, values...); err != nil {
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
	assignments := []string{s.Identifier("updated_at") + " = " + s.Placeholder(1)}
	values := []any{record.UpdatedAt}
	assignments, values, err = appendRecordUpdateMetadata(s, assignments, values, record, record.Deleted)
	if err != nil {
		return false, err
	}
	for _, field := range object.Fields {
		if recordFieldIsSystemOwned(field.Key) {
			continue
		}
		if value, ok := record.Data[field.Key]; ok {
			assignments = append(assignments, s.Identifier(field.Key)+" = "+s.Placeholder(len(values)+1))
			values = append(values, dbFieldValue(s.RuntimeEngine, field, value))
		}
	}
	where := []string{s.Identifier("workspace_id") + " = " + s.Placeholder(len(values)+1), s.Identifier("id") + " = " + s.Placeholder(len(values)+2)}
	values = append(values, workspaceID, record.ID)
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		where = append(where, s.Identifier(key)+" = "+s.Placeholder(len(values)+1))
		values = append(values, recordConditionDBValue(s.RuntimeEngine, object, key, conditions[key]))
	}
	result, err := r.database().ExecContext(ctx, "UPDATE "+s.TableIdentifier(object.Key)+" SET "+strings.Join(assignments, ", ")+" WHERE "+strings.Join(where, " AND "), values...)
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
	result, err := tx.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier(object.Key)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2), workspaceID, recordID)
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
	query := "SELECT " + s.Identifier("id") + " FROM " + s.TableIdentifier(objectKey) + " WHERE " + s.Identifier("workspace_id") + " = " + s.Placeholder(1) + " AND " + s.Identifier(fieldKey) + " = " + s.Placeholder(2)
	args := []any{workspaceID, dbValue(value)}
	if strings.TrimSpace(currentID) != "" {
		query += " AND " + s.Identifier("id") + " <> " + s.Placeholder(3)
		args = append(args, currentID)
	}
	query += " LIMIT 1"
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
		columns := []string{"workspace_id", "id", "created_at", "updated_at"}
		values := []any{workspaceID, commit.Record.ID, commit.Record.CreatedAt, commit.Record.UpdatedAt}
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
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.TableIdentifier(commit.Object.Key)+" ("+stringsJoinIdentifiers(s, columns...)+") VALUES ("+stringsJoinPlaceholders(s, len(columns))+")", values...); err != nil {
			return fmt.Errorf("insert record mutation: %w", database.MutationConstraintError(err, commit.Object.Key, commit.Record.ID, mutation.MutationConflictUnique))
		}
	case "update", "restore":
		assignments := []string{s.Identifier("updated_at") + " = " + s.Placeholder(1)}
		values := []any{commit.Record.UpdatedAt}
		assignments, values, metadataErr := appendRecordUpdateMetadata(s, assignments, values, commit.Record, operation == "restore" || commit.Record.Deleted)
		if metadataErr != nil {
			return metadataErr
		}
		for _, field := range commit.Object.Fields {
			if recordFieldIsSystemOwned(field.Key) {
				continue
			}
			if value, ok := commit.Record.Data[field.Key]; ok {
				assignments = append(assignments, s.Identifier(field.Key)+" = "+s.Placeholder(len(values)+1))
				values = append(values, dbFieldValue(s.RuntimeEngine, field, value))
			}
		}
		values = append(values, workspaceID, commit.Record.ID)
		where := s.Identifier("workspace_id") + " = " + s.Placeholder(len(values)-1) + " AND " + s.Identifier("id") + " = " + s.Placeholder(len(values))
		conditionKeys := make([]string, 0, len(commit.Conditions))
		for key := range commit.Conditions {
			if strings.TrimSpace(key) != "" && key != "id" && key != "updated_at" {
				conditionKeys = append(conditionKeys, key)
			}
		}
		sort.Strings(conditionKeys)
		for _, key := range conditionKeys {
			values = append(values, recordConditionDBValue(s.RuntimeEngine, commit.Object, key, commit.Conditions[key]))
			where += " AND " + s.Identifier(key) + " = " + s.Placeholder(len(values))
		}
		for _, predicate := range commit.Predicates {
			clause, predicateValues, err := recordMutationPredicateSQL(s, commit.Object, predicate, len(values)+1)
			if err != nil {
				return err
			}
			where += " AND " + clause
			values = append(values, predicateValues...)
		}
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			values = append(values, expected)
			where += " AND " + s.Identifier("updated_at") + " = " + s.Placeholder(len(values))
		}
		result, err := tx.ExecContext(ctx, "UPDATE "+s.TableIdentifier(commit.Object.Key)+" SET "+strings.Join(assignments, ", ")+" WHERE "+where, values...)
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
				err := tx.QueryRowContext(ctx,
					"SELECT "+s.Identifier("updated_at")+" FROM "+s.TableIdentifier(commit.Object.Key)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2)+" LIMIT 1",
					workspaceID, commit.Record.ID,
				).Scan(&current)
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
		values := []any{workspaceID, id}
		where := s.Identifier("workspace_id") + " = " + s.Placeholder(1) + " AND " + s.Identifier("id") + " = " + s.Placeholder(2)
		if expected := strings.TrimSpace(commit.OptimisticUpdatedAt()); expected != "" {
			values = append(values, expected)
			where += " AND " + s.Identifier("updated_at") + " = " + s.Placeholder(3)
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier(commit.Object.Key)+" WHERE "+where, values...)
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
		if err := r.insertAuditEventTx(ctx, tx, *commit.Audit); err != nil {
			return err
		}
	}
	for _, audit := range commit.Audits {
		if err := r.insertAuditEventTx(ctx, tx, audit); err != nil {
			return err
		}
	}
	for _, message := range commit.Outbox {
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

func (r RecordStore) validateRelatedAggregateInvariantsTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit, operation string) error {
	policies, err := recordvalidation.RecordRelatedAggregateInvariants(commit.Object)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		applies, err := recordvalidation.RecordRelatedAggregateCandidate(policy, commit.Record.Data, operation)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		relationID := strings.TrimSpace(fmt.Sprint(commit.Record.Data[policy.RelationField]))
		limitQuery := "SELECT " + r.store.Identifier(policy.LimitField) + " FROM " + r.store.TableIdentifier(policy.TargetObjectKey) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2) + " LIMIT 1"
		limitQuery += recordConstraintLockClause(r.store.RuntimeEngine)
		var rawLimit any
		if err := tx.QueryRowContext(ctx, limitQuery, workspaceID, relationID).Scan(&rawLimit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return mutation.PolicyConflict("backend.policy.related_record_missing", commit.Object.Key, commit.Record.ID, policy.RelationField)
			}
			return fmt.Errorf("lock related aggregate limit %s: %w", policy.Key, err)
		}
		columns := []string{"id"}
		if policy.Aggregate == "sum" {
			columns = append(columns, policy.ValueField)
		}
		if policy.StatusField != "" {
			columns = append(columns, policy.StatusField)
		}
		values := []any{workspaceID, relationID, commit.Record.ID}
		clauses := []string{
			r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1),
			r.store.Identifier(policy.RelationField) + " = " + r.store.Placeholder(2),
			r.store.Identifier("id") + " <> " + r.store.Placeholder(3),
		}
		if policy.StatusField != "" && len(policy.IncludedStatuses) > 0 {
			placeholders := make([]string, 0, len(policy.IncludedStatuses))
			for _, status := range policy.IncludedStatuses {
				values = append(values, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StatusField, status))
				placeholders = append(placeholders, r.store.Placeholder(len(values)))
			}
			clauses = append(clauses, r.store.Identifier(policy.StatusField)+" IN ("+strings.Join(placeholders, ", ")+")")
		}
		query := "SELECT " + stringsJoinIdentifiers(r.store, columns...) + " FROM " + r.store.TableIdentifier(commit.Object.Key) + " WHERE " + strings.Join(clauses, " AND ")
		query += recordConstraintLockClause(r.store.RuntimeEngine)
		rows, err := tx.QueryContext(ctx, query, values...)
		if err != nil {
			return fmt.Errorf("read related aggregate %s: %w", policy.Key, err)
		}
		valueField := recordAggregateValueField(commit.Object, policy)
		total, err := recordAggregateZero(valueField, policy.Aggregate)
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			var id string
			var rawValue, rawStatus any
			scans := []any{&id}
			if policy.Aggregate == "sum" {
				scans = append(scans, &rawValue)
			}
			if policy.StatusField != "" {
				scans = append(scans, &rawStatus)
			}
			if err := rows.Scan(scans...); err != nil {
				rows.Close()
				return fmt.Errorf("scan related aggregate %s: %w", policy.Key, err)
			}
			operand := any(1)
			if policy.Aggregate == "sum" {
				operand = normalizeDBValue(r.store.RuntimeEngine, valueField, rawValue)
			}
			total, err = recordAggregateAdd(total, operand, valueField, policy.Aggregate)
			if err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate related aggregate %s: %w", policy.Key, err)
		}
		rows.Close()
		candidate := any(1)
		if policy.Aggregate == "sum" {
			candidate = commit.Record.Data[policy.ValueField]
		}
		total, err = recordAggregateAdd(total, candidate, valueField, policy.Aggregate)
		if err != nil {
			return err
		}
		limit := normalizeDBValue(r.store.RuntimeEngine, valueField, rawLimit)
		matched, err := recordAggregateCompare(total, limit, valueField, policy.Operator)
		if err != nil {
			return err
		}
		if !matched {
			return mutation.PolicyConflict(policy.ErrorCode, commit.Object.Key, commit.Record.ID, policy.Key)
		}
	}
	return nil
}

func recordAggregateValueField(object definitionmodel.ObjectSchema, policy recordvalidation.RecordRelatedAggregateInvariant) definitionmodel.FieldSchema {
	if policy.Aggregate == "count" {
		return definitionmodel.FieldSchema{Key: policy.LimitField, Type: "number"}
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == policy.ValueField {
			return field
		}
	}
	return definitionmodel.FieldSchema{Key: policy.ValueField, Type: "number"}
}

func recordAggregateZero(field definitionmodel.FieldSchema, aggregate string) (any, error) {
	if aggregate == "count" || field.Type != "currency" {
		return float64(0), nil
	}
	config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
	if err != nil {
		return nil, err
	}
	return recordmodel.RecordNormalizeDecimal("0", config)
}

func recordAggregateAdd(total, operand any, field definitionmodel.FieldSchema, aggregate string) (any, error) {
	if aggregate == "sum" && field.Type == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return nil, err
		}
		left, err := recordmodel.RecordNormalizeDecimal(total, config)
		if err != nil {
			return nil, err
		}
		right, err := recordmodel.RecordNormalizeDecimal(operand, config)
		if err != nil {
			return nil, err
		}
		return recordmodel.RecordAddDecimals(left, right, config)
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(total)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(operand)), 64)
	if leftErr != nil || rightErr != nil {
		return nil, fmt.Errorf("related aggregate requires numeric values")
	}
	return left + right, nil
}

func recordAggregateCompare(total, limit any, field definitionmodel.FieldSchema, operator string) (bool, error) {
	if field.Type == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return false, err
		}
		left, err := recordmodel.RecordNormalizeDecimal(total, config)
		if err != nil {
			return false, err
		}
		right, err := recordmodel.RecordNormalizeDecimal(limit, config)
		if err != nil {
			return false, err
		}
		// RecordNormalizeDecimal already guarantees canonical decimal strings.
		leftDecimal := decimal.RequireFromString(left)
		rightDecimal := decimal.RequireFromString(right)
		comparison := leftDecimal.Cmp(rightDecimal)
		switch operator {
		case "lt":
			return comparison < 0, nil
		case "lte":
			return comparison <= 0, nil
		case "eq":
			return comparison == 0, nil
		case "gte":
			return comparison >= 0, nil
		case "gt":
			return comparison > 0, nil
		default:
			return false, fmt.Errorf("unsupported related aggregate operator %q", operator)
		}
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(total)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(limit)), 64)
	if leftErr != nil || rightErr != nil {
		return false, fmt.Errorf("related aggregate limit requires numeric values")
	}
	return recordAggregateCompareFloat(left, right, operator)
}

func recordAggregateCompareFloat(left, right float64, operator string) (bool, error) {
	switch operator {
	case "lt":
		return left < right, nil
	case "lte":
		return left <= right, nil
	case "eq":
		return left == right, nil
	case "gte":
		return left >= right, nil
	case "gt":
		return left > right, nil
	default:
		return false, fmt.Errorf("unsupported related aggregate operator %q", operator)
	}
}

func (r RecordStore) validateTemporalExclusionTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit, operation string) error {
	policies, err := recordvalidation.RecordTemporalExclusionPolicies(commit.Object)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		applies, err := recordvalidation.RecordTemporalExclusionCandidate(policy, commit.Record.Data, operation)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		clauses := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1), r.store.Identifier("id") + " <> " + r.store.Placeholder(2)}
		values := []any{workspaceID, commit.Record.ID}
		for _, field := range policy.ScopeFields {
			values = append(values, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, field, commit.Record.Data[field]))
			clauses = append(clauses, r.store.Identifier(field)+" = "+r.store.Placeholder(len(values)))
		}
		values = append(values, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StartField, commit.Record.Data[policy.EndField]))
		clauses = append(clauses, r.store.Identifier(policy.StartField)+" < "+r.store.Placeholder(len(values)))
		values = append(values, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.EndField, commit.Record.Data[policy.StartField]))
		clauses = append(clauses, r.store.Identifier(policy.EndField)+" > "+r.store.Placeholder(len(values)))
		if policy.StatusField != "" && len(policy.ExcludedStatuses) > 0 {
			placeholders := make([]string, 0, len(policy.ExcludedStatuses))
			for _, status := range policy.ExcludedStatuses {
				values = append(values, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StatusField, status))
				placeholders = append(placeholders, r.store.Placeholder(len(values)))
			}
			statusColumn := r.store.Identifier(policy.StatusField)
			clauses = append(clauses, "("+statusColumn+" IS NULL OR "+statusColumn+" NOT IN ("+strings.Join(placeholders, ", ")+"))")
		}
		query := "SELECT " + r.store.Identifier("id") + " FROM " + r.store.TableIdentifier(commit.Object.Key) + " WHERE " + strings.Join(clauses, " AND ") + " LIMIT 1"
		query += recordConstraintLockClause(r.store.RuntimeEngine)
		var conflictingID string
		err = tx.QueryRowContext(ctx, query, values...).Scan(&conflictingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("validate temporal exclusion %s: %w", policy.Key, err)
		}
		return mutation.PolicyConflict(policy.ErrorCode, commit.Object.Key, commit.Record.ID, policy.Key)
	}
	return nil
}

func recordConstraintLockClause(profile persistencedriver.EngineProfile) string {
	if profile.Capabilities().RowLock {
		return " FOR UPDATE"
	}
	return ""
}

func recordMutationPredicateSQL(store *database.RuntimeStore, object definitionmodel.ObjectSchema, predicate transactionmodel.MutationPredicate, placeholder int) (string, []any, error) {
	field := strings.TrimSpace(predicate.Field)
	allowed := field == "updated_at"
	for _, schemaField := range object.Fields {
		allowed = allowed || strings.TrimSpace(schemaField.Key) == field
	}
	operator := strings.TrimSpace(predicate.Operator)
	if !allowed {
		return "", nil, fmt.Errorf("invalid record mutation predicate field %q", field)
	}
	sqlOperator, ok := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[operator]
	if !ok {
		return "", nil, fmt.Errorf("invalid record mutation predicate operator %q", operator)
	}
	identifier := store.Identifier(field)
	if predicate.Value == nil {
		switch operator {
		case "eq":
			return identifier + " IS NULL", nil, nil
		case "ne":
			return identifier + " IS NOT NULL", nil, nil
		default:
			return "", nil, fmt.Errorf("nil predicate only supports eq/ne for field %q", field)
		}
	}
	return identifier + " " + sqlOperator + " " + store.Placeholder(placeholder), []any{recordConditionDBValue(store.RuntimeEngine, object, field, predicate.Value)}, nil
}

func recordConditionDBValue(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, key string, value any) any {
	for _, field := range object.Fields {
		if field.Key == key {
			return dbFieldValue(profile, field, value)
		}
	}
	return dbValue(value)
}

func recordQueryDBValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	if !profile.OrderedDecimalTextStorage() {
		return query
	}
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	filters := make(map[string]any, len(query.Filters))
	for key, value := range query.Filters {
		fieldKey := key
		if index := strings.LastIndex(fieldKey, "__"); index > 0 {
			fieldKey = fieldKey[:index]
		}
		field, ok := fields[fieldKey]
		if !ok || field.Type != "currency" {
			filters[key] = value
			continue
		}
		switch typed := value.(type) {
		case []any:
			encoded := make([]any, 0, len(typed))
			for _, item := range typed {
				encoded = append(encoded, dbFieldValue(profile, field, item))
			}
			filters[key] = encoded
		case []string:
			encoded := make([]string, 0, len(typed))
			for _, item := range typed {
				encoded = append(encoded, fmt.Sprint(dbFieldValue(profile, field, item)))
			}
			filters[key] = encoded
		default:
			filters[key] = dbFieldValue(profile, field, value)
		}
	}
	query.Filters = filters
	if query.FilterExpression != nil {
		encoded := recordFilterDBValues(profile, fields, *query.FilterExpression)
		query.FilterExpression = &encoded
	}
	return query
}

// RecordQueryDatabaseValues converts canonical query values to the physical
// representation used by a database-backed Record table.
func RecordQueryDatabaseValues(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	return recordQueryDBValues(profile, object, query)
}

func recordFilterDBValues(profile persistencedriver.EngineProfile, fields map[string]definitionmodel.FieldSchema, expression recordmodel.RecordFilterExpression) recordmodel.RecordFilterExpression {
	field, isCurrency := fields[expression.Field]
	isCurrency = isCurrency && field.Type == "currency"
	if isCurrency && expression.Value != nil {
		expression.Value = dbFieldValue(profile, field, expression.Value)
	}
	if isCurrency {
		for index, value := range expression.Values {
			expression.Values[index] = dbFieldValue(profile, field, value)
		}
	}
	for index := range expression.Children {
		expression.Children[index] = recordFilterDBValues(profile, fields, expression.Children[index])
	}
	return expression
}

func recordListProjection(store *database.RuntimeStore, selectFields []string) string {
	if len(selectFields) == 0 {
		return "*"
	}
	fields := ormbuilder.RecordSystemColumnNames()
	seen := make(map[string]bool, len(fields)+len(selectFields))
	for _, field := range fields {
		seen[field] = true
	}
	for _, field := range selectFields {
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return stringsJoinIdentifiers(store, fields...)
}

// ApplyRecordMutationTx lets the workflow decision adapter participate in the
// same SQL transaction without moving record mutation rules back to database.
func (r RecordStore) ApplyRecordMutationTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	return r.applyRecordMutationTx(ctx, tx, workspaceID, commit)
}

// ApplyAuditTx lets another owner adapter include explicit evidence in its
// transaction without exposing SQL to Domain/Application code.
func (r RecordStore) ApplyAuditTx(ctx context.Context, tx TransactionExecutor, event auditmodel.AuditEvent) error {
	return r.insertAuditEventTx(ctx, tx, event)
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func recordFieldIsSystemOwned(fieldKey string) bool {
	_, owned := ormbuilder.RecordSystemColumn(fieldKey)
	return owned
}

func appendRecordInsertMetadata(columns []string, values []any, record recordmodel.Record) ([]string, []any, error) {
	if record.Deleted {
		columns, values = append(columns, "deleted"), append(values, true)
	}
	if record.ExtInfo != nil {
		encoded, err := recordExtInfoDBValue(record.ExtInfo)
		if err != nil {
			return nil, nil, err
		}
		columns, values = append(columns, "ext_info"), append(values, encoded)
	}
	if userID := strings.TrimSpace(record.CreateBy); userID != "" {
		columns, values = append(columns, "create_by"), append(values, userID)
	}
	if userID := strings.TrimSpace(record.UpdateBy); userID != "" {
		columns, values = append(columns, "update_by"), append(values, userID)
	}
	return columns, values, nil
}

func appendRecordUpdateMetadata(store *database.RuntimeStore, assignments []string, values []any, record recordmodel.Record, writeDeleted bool) ([]string, []any, error) {
	if writeDeleted {
		assignments = append(assignments, store.Identifier("deleted")+" = "+store.Placeholder(len(values)+1))
		values = append(values, record.Deleted)
	}
	if record.ExtInfo != nil {
		encoded, err := recordExtInfoDBValue(record.ExtInfo)
		if err != nil {
			return nil, nil, err
		}
		assignments = append(assignments, store.Identifier("ext_info")+" = "+store.Placeholder(len(values)+1))
		values = append(values, encoded)
	}
	if userID := strings.TrimSpace(record.UpdateBy); userID != "" {
		assignments = append(assignments, store.Identifier("update_by")+" = "+store.Placeholder(len(values)+1))
		values = append(values, userID)
	}
	return assignments, values, nil
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}

func (r RecordStore) insertWorkflowIntentTx(ctx context.Context, tx TransactionExecutor, intent workflowmodel.WorkflowExecution) error {
	if strings.TrimSpace(intent.ID) == "" {
		return fmt.Errorf("mutation workflow intent requires deterministic id")
	}
	columns, values, err := workflowExecutionInsertValues(intent)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("_workflow_executions")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", values...); err != nil {
		return fmt.Errorf("insert mutation workflow intent: %w", database.MutationConstraintError(err, "workflow_execution", intent.ID, mutation.MutationConflictIdempotency))
	}
	return nil
}

func workflowExecutionInsertValues(execution workflowmodel.WorkflowExecution) ([]string, []any, error) {
	action, err := json.Marshal(execution.Action)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payload, err := json.Marshal(execution.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	result, err := json.Marshal(execution.Result)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow result: %w", err)
	}
	columns := []string{"workspace_id", "id", "workflow_key", "name", "trigger", "status", "action_type", "action_json", "payload_json", "result_json", "process_id", "node_id", "object_key", "record_id", "actor_id", "run_as", "idempotency_key", "attempt", "max_attempts", "next_run_at", "last_error", "message", "created_at", "updated_at"}
	values := []any{execution.WorkspaceID, execution.ID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(action), string(payload), string(result), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, database.NullableText(execution.NextRunAt), execution.LastError, execution.Message, execution.CreatedAt, execution.UpdatedAt}
	return columns, values, nil
}

func (r RecordStore) insertAuditEventTx(ctx context.Context, tx TransactionExecutor, event auditmodel.AuditEvent) error {
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("mutation audit requires deterministic id")
	}
	before, err := json.Marshal(event.Before)
	if err != nil {
		return fmt.Errorf("encode mutation audit before: %w", err)
	}
	after, err := json.Marshal(event.After)
	if err != nil {
		return fmt.Errorf("encode mutation audit after: %w", err)
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode mutation audit metadata: %w", err)
	}
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	values := []any{event.ID, event.WorkspaceID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary, string(metadata), string(before), string(after), event.CreatedAt}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("_audit_events")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", values...); err != nil {
		return fmt.Errorf("insert mutation audit: %w", database.MutationConstraintError(err, "audit_event", event.ID, mutation.MutationConflictIdempotency))
	}
	return nil
}

func (r RecordStore) insertIntegrationOutboxTx(ctx context.Context, tx TransactionExecutor, message integrationmodel.IntegrationOutboxMessage) error {
	s := r.store
	message.Payload = telemetry.EnsureAsyncPayload(ctx, message.Payload)
	now := time.Now().UTC().Format(time.RFC3339)
	message.WorkspaceID = integrationpersistence.WorkspaceID(message.WorkspaceID)
	message.DedupKey = strings.TrimSpace(message.DedupKey)
	if message.DedupKey == "" {
		return fmt.Errorf("mutation outbox requires stable dedup key")
	}
	if strings.TrimSpace(message.ID) == "" {
		message.ID = integrationpersistence.OutboxDedupID(message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.DedupKey)
	}
	if strings.TrimSpace(message.Status) == "" {
		message.Status = "queued"
	}
	message.CreatedAt = now
	message.UpdatedAt = now
	payload, err := json.Marshal(database.NonNilMap(message.Payload))
	if err != nil {
		return fmt.Errorf("encode mutation outbox payload: %w", err)
	}
	columns := []string{"id", "workspace_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at"}
	values := []any{message.ID, message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.Status, string(payload), message.EventID, message.RequestRef, message.DedupKey, message.RequestFingerprint, message.ResponseRef, message.Error, message.AttemptCount, message.NextAttemptAt, message.LastAttemptAt, message.LeaseOwner, message.LeaseExpiresAt, message.FencingToken, message.CreatedBy, message.CreatedAt, message.UpdatedAt}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+s.TableIdentifier("integration_outbox_messages")+" ("+stringsJoinIdentifiers(s, columns...)+") VALUES ("+stringsJoinPlaceholders(s, len(columns))+")", values...); err != nil {
		return fmt.Errorf("insert mutation outbox: %w", database.MutationConstraintError(err, "integration_outbox", message.ID, mutation.MutationConflictIdempotency))
	}
	if err := integrationpersistence.RegisterOutboxWorkerQueueScope(ctx, r.store, tx, message.WorkspaceID, now); err != nil {
		return err
	}
	if transactioncontract.ActiveTransaction(ctx) {
		locator := workerplatform.DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: message.WorkspaceID, TaskID: message.ID}
		if err := transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "wake integration outbox " + message.ID, Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true,
			Run: func(context.Context) error { r.store.WorkerWakeups().Publish(locator); return nil },
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r RecordStore) PublishCommittedOutboxWakeups(_ context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) {
	for _, commit := range commits {
		for _, message := range commit.Outbox {
			r.publishIntegrationOutboxWakeup(workspaceID, message)
		}
	}
}

func (r RecordStore) PublishCommittedNotificationWakeups(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) {
	notifications := notificationpersistence.NewInboxEventWriter(r.store)
	for _, commit := range commits {
		for _, event := range commit.NotificationEvents {
			event.WorkspaceID = workspaceID
			notifications.PublishCommittedEventWakeup(event)
		}
	}
}

func (r RecordStore) publishIntegrationOutboxWakeup(workspaceID string, message integrationmodel.IntegrationOutboxMessage) {
	resolvedWorkspace := strings.TrimSpace(message.WorkspaceID)
	if len(resolvedWorkspace) == 0 {
		resolvedWorkspace = workspaceID
	}
	message.WorkspaceID = integrationpersistence.WorkspaceID(resolvedWorkspace)
	if strings.TrimSpace(message.ID) == "" {
		message.ID = integrationpersistence.OutboxDedupID(message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.DedupKey)
	}
	r.store.WorkerWakeups().Publish(workerplatform.DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: message.WorkspaceID, TaskID: message.ID})
}
