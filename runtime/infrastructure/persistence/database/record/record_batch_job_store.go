package record

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

var recordBatchJobColumns = []string{
	"id", "workspace_id", "kind", "object_key", "status", "idempotency_key", "request_fingerprint", "payload_json",
	"checkpoint_value", "total_value", "result_filename", "result_content_type", "result_chunks", "error_code", "checkpoint_cursor", "audit_id", "result_artifact_id",
	"attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "actor_id", "role_key", "created_at", "updated_at",
}

func recordBatchJobSelect(store *database.RuntimeStore, workspaceID string) *ormbuilder.SelectBuilder {
	return ormbuilder.NewWorkspaceSelectBuilder(store.SQLRenderer, "record_batch_jobs", strings.TrimSpace(workspaceID)).Columns(recordBatchJobColumns...)
}

func recordBatchDuePredicate(now string) ormbuilder.Predicate {
	return ormbuilder.Or(
		ormbuilder.And(ormbuilder.Equal("status", "queued"), ormbuilder.Or(ormbuilder.Equal("next_attempt_at", ""), ormbuilder.LessThanOrEqual("next_attempt_at", now))),
		ormbuilder.And(ormbuilder.Equal("status", "running"), ormbuilder.LessThanOrEqual("lease_expires_at", now)),
	)
}

func recordBatchLeasePredicate(job recordmodel.RecordBatchJob) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("id", job.ID), ormbuilder.Equal("status", "running"), ormbuilder.Equal("lease_owner", job.LeaseOwner), ormbuilder.Equal("fencing_token", job.FencingToken))
}

func (r RecordStore) EnqueueRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob) (recordmodel.RecordBatchJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	job.WorkspaceID = strings.TrimSpace(job.WorkspaceID)
	job.Kind, job.ObjectKey, job.IdempotencyKey = strings.TrimSpace(job.Kind), strings.TrimSpace(job.ObjectKey), strings.TrimSpace(job.IdempotencyKey)
	if len(job.WorkspaceID) == 0 {
		return recordmodel.RecordBatchJob{}, false, fmt.Errorf("record batch job workspace is required")
	}
	if job.Kind == "" || job.ObjectKey == "" || job.IdempotencyKey == "" {
		return recordmodel.RecordBatchJob{}, false, fmt.Errorf("record batch job identity is required")
	}
	if job.ID == "" {
		digest := sha256.Sum256([]byte(job.WorkspaceID + "\x00" + job.Kind + "\x00" + job.ObjectKey + "\x00" + job.IdempotencyKey))
		job.ID = "record_batch:" + hex.EncodeToString(digest[:12])
	}
	fingerprint := sha256.Sum256([]byte(job.Kind + "\x00" + job.ObjectKey + "\x00" + job.PayloadJSON))
	job.Fingerprint = hex.EncodeToString(fingerprint[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	job.Status, job.CreatedAt, job.UpdatedAt = "queued", now, now
	if err := r.registerRecordBatchWorkerQueueScope(ctx, job.WorkspaceID, now); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	columns := append(append([]string{}, recordBatchJobColumns[:1]...), recordBatchJobColumns[2:]...)
	values := recordBatchJobValues(job)
	values = append(append([]any{}, values[:1]...), values[2:]...)
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	if _, err := r.database().ExecContext(ctx, query, args...); err != nil {
		existing, found, findErr := r.findRecordBatchJobByScope(ctx, job.WorkspaceID, job.Kind, job.ObjectKey, job.IdempotencyKey)
		if findErr != nil {
			return recordmodel.RecordBatchJob{}, false, fmt.Errorf("resolve record batch enqueue conflict: %w", findErr)
		}
		if found {
			if existing.Fingerprint != job.Fingerprint {
				return recordmodel.RecordBatchJob{}, false, recordcontract.ErrRecordBatchJobIdempotencyConflict
			}
			return existing, true, nil
		}
		return recordmodel.RecordBatchJob{}, false, fmt.Errorf("enqueue record batch job: %w", err)
	}
	return job, false, nil
}

func (r RecordStore) GetRecordBatchJob(ctx context.Context, workspaceID, jobID string) (recordmodel.RecordBatchJob, bool, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query, args, buildErr := recordBatchJobSelect(r.store, workspaceID).Where(ormbuilder.Equal("id", strings.TrimSpace(jobID))).Limit(1).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	job, err := scanRecordBatchJob(r.queryExecutor(ctx).QueryRowContext(ctx, query, args...))
	if err != nil {
		if errorsIsNoRows(err) {
			return recordmodel.RecordBatchJob{}, false, nil
		}
		return recordmodel.RecordBatchJob{}, false, err
	}
	return job, true, nil
}

func (r RecordStore) FindRecordBatchJobByIdempotency(ctx context.Context, workspaceID, kind, objectKey, key string) (recordmodel.RecordBatchJob, bool, error) {
	return r.findRecordBatchJobByScope(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(kind), strings.TrimSpace(objectKey), strings.TrimSpace(key))
}

func (r RecordStore) FindLatestRecordBatchJobByFingerprint(ctx context.Context, workspaceID, kind, objectKey, fingerprint string) (recordmodel.RecordBatchJob, bool, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query, args, buildErr := recordBatchJobSelect(r.store, workspaceID).Where(ormbuilder.And(ormbuilder.Equal("kind", strings.TrimSpace(kind)), ormbuilder.Equal("object_key", strings.TrimSpace(objectKey)), ormbuilder.Equal("request_fingerprint", strings.TrimSpace(fingerprint)))).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(1).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	job, err := scanRecordBatchJob(r.queryExecutor(ctx).QueryRowContext(ctx, query, args...))
	if errorsIsNoRows(err) {
		return recordmodel.RecordBatchJob{}, false, nil
	}
	return job, err == nil, err
}

func (r RecordStore) ClaimRecordBatchJobs(ctx context.Context, limit int, owner string, leaseTTL time.Duration, now time.Time) ([]recordmodel.RecordBatchJob, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || now.IsZero() {
		return nil, fmt.Errorf("record batch worker owner and clock are required")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if leaseTTL <= 0 {
		leaseTTL = time.Minute
	}
	now = now.UTC()
	nowText, expires := now.Format(time.RFC3339Nano), now.Add(leaseTTL).Format(time.RFC3339Nano)
	candidates := []recordmodel.RecordBatchJob{}
	workspaces, err := r.store.WorkerQueueScopePage(ctx, r.database(), "record_batch", recordBatchWorkspaceScanLimit(limit))
	if err != nil {
		return nil, err
	}
	for _, workspaceID := range workspaces {
		workspaceCtx := recordBatchWorkspaceContext(ctx, workspaceID, owner)
		query, args, buildErr := recordBatchJobSelect(r.store, workspaceID).Where(recordBatchDuePredicate(nowText)).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit * 4).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		rows, queryErr := r.database().QueryContext(workspaceCtx, query, args...)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			job, scanErr := scanRecordBatchJob(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			candidates = append(candidates, job)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	candidates = capacity.FairOrder(candidates, limit, func(job recordmodel.RecordBatchJob) string { return job.WorkspaceID + "\x00" + job.Kind })
	claimed := make([]recordmodel.RecordBatchJob, 0, limit)
	for _, candidate := range candidates {
		candidateCtx := recordBatchWorkspaceContext(ctx, candidate.WorkspaceID, owner)
		update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", candidate.WorkspaceID).Set("status", "running").Set("lease_owner", owner).Set("lease_expires_at", expires).SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).Set("updated_at", nowText).Where(ormbuilder.And(ormbuilder.Equal("id", candidate.ID), recordBatchDuePredicate(nowText))).Build()
		if buildErr != nil {
			return claimed, buildErr
		}
		result, updateErr := r.database().ExecContext(candidateCtx, update, updateArgs...)
		if updateErr != nil {
			return claimed, updateErr
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return claimed, affectedErr
		}
		if affected == 0 {
			continue
		}
		candidate.Status = "running"
		candidate.LeaseOwner = owner
		candidate.LeaseExpiresAt = expires
		candidate.FencingToken++
		candidate.AttemptCount++
		candidate.UpdatedAt = nowText
		claimed = append(claimed, candidate)
		if len(claimed) == limit {
			break
		}
	}
	return claimed, nil
}

func recordBatchWorkspaceScanLimit(jobLimit int) int {
	return min(256, max(32, jobLimit*2))
}

func (r RecordStore) SaveRecordBatchJobCheckpoint(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	return r.updateClaimedRecordBatchJob(ctx, job, "running", now)
}

// CommitRecordBatchJobPage atomically appends one deterministic result chunk
// and advances the opaque cursor under the current lease/fencing token. A
// retry therefore resumes after the last committed page without duplicates.
func (r RecordStore) CommitRecordBatchJobPage(ctx context.Context, job recordmodel.RecordBatchJob, expectedCursor string, chunk recordmodel.RecordBatchJobChunk, nextCursor string, processed, total int, now time.Time) error {
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	tx, err := r.database().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	guard, guardArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Columns("id").Where(ormbuilder.And(recordBatchLeasePredicate(job), ormbuilder.Equal("checkpoint_cursor", expectedCursor))).Build()
	if buildErr != nil {
		return buildErr
	}
	var guarded string
	if err := tx.QueryRowContext(ctx, guard, guardArgs...).Scan(&guarded); err != nil {
		return fmt.Errorf("record batch page lease or cursor lost: %w", err)
	}
	sequence := job.ResultChunks
	insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "record_batch_job_chunks", job.WorkspaceID).Columns("job_id", "sequence_no", "content", "created_at").Values(job.ID, sequence, chunk.Content, now.UTC().Format(time.RFC3339Nano)).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
		return err
	}
	job.Checkpoint, job.Total, job.CheckpointCursor, job.ResultChunks = processed, total, nextCursor, sequence+1
	if err := r.updateClaimedRecordBatchJob(txCtx, job, "running", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r RecordStore) HeartbeatRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, leaseTTL time.Duration, now time.Time) error {
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	if leaseTTL <= 0 {
		leaseTTL = time.Minute
	}
	now = now.UTC()
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Set("lease_expires_at", now.Add(leaseTTL).Format(time.RFC3339Nano)).Set("updated_at", now.Format(time.RFC3339Nano)).Where(recordBatchLeasePredicate(job)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("record batch job lease lost")
	}
	return nil
}

func (r RecordStore) RetryRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, next, now time.Time) error {
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	nowText := now.UTC().Format(time.RFC3339Nano)
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Set("status", "queued").Set("next_attempt_at", next.UTC().Format(time.RFC3339Nano)).Set("error_code", job.ErrorCode).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", nowText).Where(recordBatchLeasePredicate(job)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("record batch job lease lost")
	}
	return nil
}

func (r RecordStore) CompleteRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	return r.updateClaimedRecordBatchJob(ctx, job, "completed", now)
}

func (r RecordStore) FailRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	return r.updateClaimedRecordBatchJob(ctx, job, "failed", now)
}

func (r RecordStore) QuarantineRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	return r.updateClaimedRecordBatchJob(ctx, job, "quarantined", now)
}

func (r RecordStore) RequeueRecordBatchJob(ctx context.Context, workspaceID, id string, now time.Time) (recordmodel.RecordBatchJob, bool, error) {
	workspaceID, id = strings.TrimSpace(workspaceID), strings.TrimSpace(id)
	if workspaceID == "" || id == "" || now.IsZero() {
		return recordmodel.RecordBatchJob{}, false, fmt.Errorf("record batch requeue identity is invalid")
	}
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	nowText := now.UTC().Format(time.RFC3339Nano)
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", workspaceID).Set("status", "queued").Set("next_attempt_at", nowText).Set("lease_owner", "").Set("lease_expires_at", "").SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", nowText).Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.In("status", "failed", "quarantined"))).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	job, found, err := r.GetRecordBatchJob(ctx, workspaceID, id)
	return job, found && affected == 1, err
}

func (r RecordStore) updateClaimedRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, status string, now time.Time) error {
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	nowText := now.UTC().Format(time.RFC3339Nano)
	leaseOwner, leaseExpires := job.LeaseOwner, job.LeaseExpiresAt
	if status != "running" {
		leaseOwner, leaseExpires = "", ""
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Set("status", status).Set("checkpoint_value", job.Checkpoint).Set("total_value", job.Total).Set("result_filename", job.ResultFilename).Set("result_content_type", job.ResultType).Set("result_chunks", job.ResultChunks).Set("error_code", job.ErrorCode).Set("checkpoint_cursor", job.CheckpointCursor).Set("audit_id", job.AuditID).Set("result_artifact_id", job.ResultArtifactID).Set("lease_owner", leaseOwner).Set("lease_expires_at", leaseExpires).Set("updated_at", nowText).Where(recordBatchLeasePredicate(job)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := r.queryExecutor(ctx).ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("record batch job lease lost")
	}
	return nil
}

func (r RecordStore) CancelRecordBatchJob(ctx context.Context, workspaceID, jobID string) (recordmodel.RecordBatchJob, bool, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "record_batch_jobs", workspaceID).Set("status", "cancelled").Set("lease_owner", "").Set("lease_expires_at", "").SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("id", strings.TrimSpace(jobID)), ormbuilder.In("status", "queued", "running", "failed", "quarantined"))).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	if _, err := r.queryExecutor(ctx).ExecContext(ctx, query, args...); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	return r.GetRecordBatchJob(ctx, workspaceID, jobID)
}

func (r RecordStore) ReplaceRecordBatchJobChunks(ctx context.Context, job recordmodel.RecordBatchJob, chunks []recordmodel.RecordBatchJobChunk, now time.Time) error {
	ctx = recordBatchWorkspaceContext(ctx, job.WorkspaceID, job.ActorID)
	tx, err := r.database().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	guard, guardArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "record_batch_jobs", job.WorkspaceID).Columns("id").Where(recordBatchLeasePredicate(job)).Build()
	if buildErr != nil {
		return buildErr
	}
	var guarded string
	if err := tx.QueryRowContext(ctx, guard, guardArgs...).Scan(&guarded); err != nil {
		return fmt.Errorf("record batch job chunk lease lost: %w", err)
	}
	deleteQuery, deleteArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, "record_batch_job_chunks", job.WorkspaceID).Where(ormbuilder.Equal("job_id", job.ID)).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, deleteQuery, deleteArgs...); err != nil {
		return err
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	for sequence, chunk := range chunks {
		insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "record_batch_job_chunks", job.WorkspaceID).Columns("job_id", "sequence_no", "content", "created_at").Values(job.ID, sequence, chunk.Content, nowText).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r RecordStore) ListRecordBatchJobChunks(ctx context.Context, workspaceID, jobID string) ([]recordmodel.RecordBatchJobChunk, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "record_batch_job_chunks", workspaceID).Columns("workspace_id", "job_id", "sequence_no", "content").Where(ormbuilder.Equal("job_id", strings.TrimSpace(jobID))).OrderBy(ormbuilder.Ascending("sequence_no")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []recordmodel.RecordBatchJobChunk{}
	for rows.Next() {
		var chunk recordmodel.RecordBatchJobChunk
		if err := rows.Scan(&chunk.WorkspaceID, &chunk.JobID, &chunk.Sequence, &chunk.Content); err != nil {
			return nil, err
		}
		out = append(out, chunk)
	}
	return out, rows.Err()
}

func (r RecordStore) findRecordBatchJobByScope(ctx context.Context, workspaceID, kind, objectKey, key string) (recordmodel.RecordBatchJob, bool, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query, args, buildErr := recordBatchJobSelect(r.store, workspaceID).Where(ormbuilder.And(ormbuilder.Equal("kind", kind), ormbuilder.Equal("object_key", objectKey), ormbuilder.Equal("idempotency_key", key))).Limit(1).Build()
	if buildErr != nil {
		return recordmodel.RecordBatchJob{}, false, buildErr
	}
	job, err := scanRecordBatchJob(r.database().QueryRowContext(ctx, query, args...))
	if err != nil {
		if errorsIsNoRows(err) {
			return recordmodel.RecordBatchJob{}, false, nil
		}
		return recordmodel.RecordBatchJob{}, false, err
	}
	return job, true, nil
}

type recordBatchJobScanner interface{ Scan(...any) error }

func scanRecordBatchJob(scanner recordBatchJobScanner) (recordmodel.RecordBatchJob, error) {
	var job recordmodel.RecordBatchJob
	err := scanner.Scan(&job.ID, &job.WorkspaceID, &job.Kind, &job.ObjectKey, &job.Status, &job.IdempotencyKey, &job.Fingerprint, &job.PayloadJSON, &job.Checkpoint, &job.Total, &job.ResultFilename, &job.ResultType, &job.ResultChunks, &job.ErrorCode, &job.CheckpointCursor, &job.AuditID, &job.ResultArtifactID, &job.AttemptCount, &job.NextAttemptAt, &job.LeaseOwner, &job.LeaseExpiresAt, &job.FencingToken, &job.ActorID, &job.RoleKey, &job.CreatedAt, &job.UpdatedAt)
	return job, err
}

func recordBatchJobValues(job recordmodel.RecordBatchJob) []any {
	return []any{job.ID, job.WorkspaceID, job.Kind, job.ObjectKey, job.Status, job.IdempotencyKey, job.Fingerprint, job.PayloadJSON, job.Checkpoint, job.Total, job.ResultFilename, job.ResultType, job.ResultChunks, job.ErrorCode, job.CheckpointCursor, job.AuditID, job.ResultArtifactID, job.AttemptCount, job.NextAttemptAt, job.LeaseOwner, job.LeaseExpiresAt, job.FencingToken, job.ActorID, job.RoleKey, job.CreatedAt, job.UpdatedAt}
}

func errorsIsNoRows(err error) bool {
	return err != nil && (err == sql.ErrNoRows || strings.Contains(strings.ToLower(err.Error()), "no rows"))
}

func (r RecordStore) RecordBatchJobQueueStats(ctx context.Context, workspaceID string) (int, time.Duration, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return 0, 0, fmt.Errorf("record batch job workspace is required")
	}
	return r.recordBatchJobQueueStats(recordBatchWorkspaceContext(ctx, workspaceID, ""), workspaceID)
}

func (r RecordStore) GlobalRecordBatchJobQueueStats(ctx context.Context, scope principalmodel.SystemScope) (int, time.Duration, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return 0, 0, err
	}
	workspaces, err := r.recordBatchWorkerQueueScopes(ctx)
	if err != nil {
		return 0, 0, err
	}
	depth, oldest := 0, time.Duration(0)
	for _, workspaceID := range workspaces {
		workspaceDepth, workspaceOldest, statsErr := r.recordBatchJobQueueStats(recordBatchWorkspaceContext(ctx, workspaceID, "record-batch-metrics"), workspaceID)
		if statsErr != nil {
			return 0, 0, statsErr
		}
		depth += workspaceDepth
		if workspaceOldest > oldest {
			oldest = workspaceOldest
		}
	}
	return depth, oldest, nil
}

func (r RecordStore) recordBatchJobQueueStats(ctx context.Context, workspaceID string) (int, time.Duration, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return 0, 0, fmt.Errorf("record batch job workspace is required")
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "record_batch_jobs", workspaceID).
		Projections(
			ormbuilder.Project(ormbuilder.CountAll()),
			ormbuilder.Project(ormbuilder.Coalesce(ormbuilder.Min(ormbuilder.Column("created_at")), ormbuilder.Value(""))),
		).
		Where(ormbuilder.Equal("status", "queued")).
		Build()
	if buildErr != nil {
		return 0, 0, fmt.Errorf("build record batch queue stats: %w", buildErr)
	}
	var depth int
	var oldest string
	if err := r.database().QueryRowContext(ctx, query, args...).Scan(&depth, &oldest); err != nil {
		return 0, 0, err
	}
	created, err := time.Parse(time.RFC3339Nano, oldest)
	if err != nil || depth == 0 {
		return depth, 0, nil
	}
	age := time.Since(created)
	if age < 0 {
		age = 0
	}
	return depth, age, nil
}

func (r RecordStore) registerRecordBatchWorkerQueueScope(ctx context.Context, workspaceID, updatedAt string) error {
	digest := sha256.Sum256([]byte("record_batch\x00" + strings.TrimSpace(workspaceID)))
	id := "worker_scope:" + hex.EncodeToString(digest[:12])
	insert := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "runtime_worker_queue_scopes").
		Columns("id", "queue_kind", "scope_key", "updated_at").Values(id, "record_batch", strings.TrimSpace(workspaceID), updatedAt)
	insert, err := r.store.Engine.ApplyUpsert(insert, []string{"id"},
		ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")),
	)
	if err != nil {
		return fmt.Errorf("build record batch worker queue scope: %w", err)
	}
	query, args, err := insert.Build()
	if err != nil {
		return fmt.Errorf("build record batch worker queue scope: %w", err)
	}
	if _, err := r.database().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("register record batch worker queue scope: %w", err)
	}
	return nil
}

func (r RecordStore) recordBatchWorkerQueueScopes(ctx context.Context) ([]string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "runtime_worker_queue_scopes").Columns("scope_key").Where(ormbuilder.Equal("queue_kind", "record_batch")).OrderBy(ormbuilder.Ascending("scope_key")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			values = append(values, workspaceID)
		}
	}
	return values, rows.Err()
}

func recordBatchWorkspaceContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if strings.TrimSpace(actorID) != "" {
		ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(actorID))
	}
	return ctx
}
