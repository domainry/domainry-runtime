package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

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
