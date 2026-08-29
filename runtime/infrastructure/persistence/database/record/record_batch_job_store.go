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
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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

func recordBatchWorkspaceContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if strings.TrimSpace(actorID) != "" {
		ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(actorID))
	}
	return ctx
}
