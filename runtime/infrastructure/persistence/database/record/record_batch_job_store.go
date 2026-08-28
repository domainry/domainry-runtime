package record

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

var recordBatchJobColumns = []string{
	"id", "workspace_id", "kind", "object_key", "status", "idempotency_key", "request_fingerprint", "payload_json",
	"checkpoint_value", "total_value", "result_filename", "result_content_type", "result_chunks", "error_code", "checkpoint_cursor", "audit_id", "result_artifact_id",
	"attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "actor_id", "role_key", "created_at", "updated_at",
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
	columns := stringsJoinIdentifiers(r.store, recordBatchJobColumns...)
	query := "INSERT INTO " + r.store.TableIdentifier("record_batch_jobs") + " (" + columns + ") VALUES (" + stringsJoinPlaceholders(r.store, len(recordBatchJobColumns)) + ")"
	if _, err := r.database().ExecContext(ctx, query, recordBatchJobValues(job)...); err != nil {
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
	query := "SELECT " + stringsJoinIdentifiers(r.store, recordBatchJobColumns...) + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2) + " LIMIT 1"
	job, err := scanRecordBatchJob(r.queryExecutor(ctx).QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(jobID)))
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
	query := "SELECT " + stringsJoinIdentifiers(r.store, recordBatchJobColumns...) + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("kind") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("object_key") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("request_fingerprint") + " = " + r.store.Placeholder(4) + " ORDER BY " + r.store.Identifier("created_at") + " DESC LIMIT 1"
	job, err := scanRecordBatchJob(r.queryExecutor(ctx).QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(kind), strings.TrimSpace(objectKey), strings.TrimSpace(fingerprint)))
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
		query := "SELECT " + stringsJoinIdentifiers(r.store, recordBatchJobColumns...) + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND ((" + r.store.Identifier("status") + " = 'queued' AND (" + r.store.Identifier("next_attempt_at") + " = '' OR " + r.store.Identifier("next_attempt_at") + " <= " + r.store.Placeholder(2) + ")) OR (" + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(3) + ")) ORDER BY " + r.store.Identifier("created_at") + " ASC LIMIT " + r.store.Placeholder(4)
		rows, queryErr := r.database().QueryContext(workspaceCtx, query, workspaceID, nowText, nowText, limit*4)
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
		update := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("status") + " = 'running', " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("attempt_count") + " = " + r.store.Identifier("attempt_count") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(5) + " AND ((" + r.store.Identifier("status") + " = 'queued' AND (" + r.store.Identifier("next_attempt_at") + " = '' OR " + r.store.Identifier("next_attempt_at") + " <= " + r.store.Placeholder(6) + ")) OR (" + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(7) + "))"
		result, updateErr := r.database().ExecContext(candidateCtx, update, owner, expires, nowText, candidate.WorkspaceID, candidate.ID, nowText, nowText)
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
	guard := "SELECT " + r.store.Identifier("id") + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("checkpoint_cursor") + " = " + r.store.Placeholder(5)
	var guarded string
	if err := tx.QueryRowContext(ctx, guard, job.WorkspaceID, job.ID, job.LeaseOwner, job.FencingToken, expectedCursor).Scan(&guarded); err != nil {
		return fmt.Errorf("record batch page lease or cursor lost: %w", err)
	}
	sequence := job.ResultChunks
	insert := "INSERT INTO " + r.store.TableIdentifier("record_batch_job_chunks") + " (" + stringsJoinIdentifiers(r.store, "workspace_id", "job_id", "sequence_no", "content", "created_at") + ") VALUES (" + stringsJoinPlaceholders(r.store, 5) + ")"
	if _, err := tx.ExecContext(ctx, insert, job.WorkspaceID, job.ID, sequence, chunk.Content, now.UTC().Format(time.RFC3339Nano)); err != nil {
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
	query := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(6)
	result, err := r.database().ExecContext(ctx, query, now.Add(leaseTTL).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), job.WorkspaceID, job.ID, job.LeaseOwner, job.FencingToken)
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
	query := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("status") + " = 'queued', " + r.store.Identifier("next_attempt_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("error_code") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(7)
	result, err := r.database().ExecContext(ctx, query, next.UTC().Format(time.RFC3339Nano), job.ErrorCode, nowText, job.WorkspaceID, job.ID, job.LeaseOwner, job.FencingToken)
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
	query := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("status") + " = 'queued', " + r.store.Identifier("next_attempt_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("status") + " IN ('failed','quarantined')"
	result, err := r.database().ExecContext(ctx, query, nowText, nowText, workspaceID, id)
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
	query := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("checkpoint_value") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("total_value") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("result_filename") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("result_content_type") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("result_chunks") + " = " + r.store.Placeholder(6) + ", " + r.store.Identifier("error_code") + " = " + r.store.Placeholder(7) + ", " + r.store.Identifier("checkpoint_cursor") + " = " + r.store.Placeholder(8) + ", " + r.store.Identifier("audit_id") + " = " + r.store.Placeholder(9) + ", " + r.store.Identifier("result_artifact_id") + " = " + r.store.Placeholder(10) + ", " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(11) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(12) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(13) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(14) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(15) + " AND " + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(16) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(17)
	result, err := r.queryExecutor(ctx).ExecContext(ctx, query, status, job.Checkpoint, job.Total, job.ResultFilename, job.ResultType, job.ResultChunks, job.ErrorCode, job.CheckpointCursor, job.AuditID, job.ResultArtifactID, leaseOwner, leaseExpires, nowText, job.WorkspaceID, job.ID, job.LeaseOwner, job.FencingToken)
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
	query := "UPDATE " + r.store.TableIdentifier("record_batch_jobs") + " SET " + r.store.Identifier("status") + " = 'cancelled', " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(1) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("status") + " IN ('queued','running','failed','quarantined')"
	if _, err := r.queryExecutor(ctx).ExecContext(ctx, query, now, strings.TrimSpace(workspaceID), strings.TrimSpace(jobID)); err != nil {
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
	guard := "SELECT " + r.store.Identifier("id") + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("status") + " = 'running' AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(4)
	var guarded string
	if err := tx.QueryRowContext(ctx, guard, job.WorkspaceID, job.ID, job.LeaseOwner, job.FencingToken).Scan(&guarded); err != nil {
		return fmt.Errorf("record batch job chunk lease lost: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("record_batch_job_chunks")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("job_id")+" = "+r.store.Placeholder(2), job.WorkspaceID, job.ID); err != nil {
		return err
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	for sequence, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("record_batch_job_chunks")+" ("+stringsJoinIdentifiers(r.store, "workspace_id", "job_id", "sequence_no", "content", "created_at")+") VALUES ("+stringsJoinPlaceholders(r.store, 5)+")", job.WorkspaceID, job.ID, sequence, chunk.Content, nowText); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r RecordStore) ListRecordBatchJobChunks(ctx context.Context, workspaceID, jobID string) ([]recordmodel.RecordBatchJobChunk, error) {
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query := "SELECT " + stringsJoinIdentifiers(r.store, "workspace_id", "job_id", "sequence_no", "content") + " FROM " + r.store.TableIdentifier("record_batch_job_chunks") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("job_id") + " = " + r.store.Placeholder(2) + " ORDER BY " + r.store.Identifier("sequence_no") + " ASC"
	rows, err := r.database().QueryContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(jobID))
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
	query := "SELECT " + stringsJoinIdentifiers(r.store, recordBatchJobColumns...) + " FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("kind") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("object_key") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("idempotency_key") + " = " + r.store.Placeholder(4) + " LIMIT 1"
	job, err := scanRecordBatchJob(r.database().QueryRowContext(ctx, query, workspaceID, kind, objectKey, key))
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
	query := "SELECT COUNT(*), COALESCE(MIN(" + r.store.Identifier("created_at") + "), '') FROM " + r.store.TableIdentifier("record_batch_jobs") + " WHERE " + r.store.Identifier("status") + " = 'queued'"
	args := []any{}
	if workspaceID != "" {
		query += " AND " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)
		args = append(args, workspaceID)
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
	query := "INSERT INTO " + r.store.TableIdentifier("runtime_worker_queue_scopes") + " (" + stringsJoinIdentifiers(r.store, "id", "queue_kind", "scope_key", "updated_at") + ") VALUES (" + stringsJoinPlaceholders(r.store, 4) + ")"
	if r.store.Driver() == "mysql" {
		query += " ON DUPLICATE KEY UPDATE " + r.store.Identifier("updated_at") + " = VALUES(" + r.store.Identifier("updated_at") + ")"
	} else {
		query += " ON CONFLICT DO NOTHING"
	}
	if _, err := r.database().ExecContext(ctx, query, id, "record_batch", strings.TrimSpace(workspaceID), updatedAt); err != nil {
		return fmt.Errorf("register record batch worker queue scope: %w", err)
	}
	if r.store.Driver() != "mysql" {
		update := "UPDATE " + r.store.TableIdentifier("runtime_worker_queue_scopes") + " SET " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(1) + " WHERE " + r.store.Identifier("queue_kind") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("scope_key") + " = " + r.store.Placeholder(3)
		if _, err := r.database().ExecContext(ctx, update, updatedAt, "record_batch", strings.TrimSpace(workspaceID)); err != nil {
			return fmt.Errorf("refresh record batch worker queue scope: %w", err)
		}
	}
	return nil
}

func (r RecordStore) recordBatchWorkerQueueScopes(ctx context.Context) ([]string, error) {
	query := "SELECT " + r.store.Identifier("scope_key") + " FROM " + r.store.TableIdentifier("runtime_worker_queue_scopes") + " WHERE " + r.store.Identifier("queue_kind") + " = " + r.store.Placeholder(1) + " ORDER BY " + r.store.Identifier("scope_key") + " ASC"
	rows, err := r.database().QueryContext(ctx, query, "record_batch")
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
