package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

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
