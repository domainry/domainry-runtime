package record

import (
	"context"
	"database/sql"
	"fmt"
	"io"
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

func (r RecordStore) OpenRecordBatchJobSource(ctx context.Context, workspaceID, jobID string, expectedChunks int) (io.ReadCloser, error) {
	if expectedChunks < 0 {
		return nil, fmt.Errorf("record batch source chunk count is invalid")
	}
	ctx = recordBatchWorkspaceContext(ctx, workspaceID, "")
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "record_batch_job_chunks", workspaceID).
		Columns("sequence_no", "content").Where(ormbuilder.Equal("job_id", strings.TrimSpace(jobID))).OrderBy(ormbuilder.Ascending("sequence_no")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &recordBatchChunkSourceReader{rows: rows, expected: expectedChunks}, nil
}

type recordBatchChunkSourceReader struct {
	rows     *sql.Rows
	expected int
	next     int
	current  *strings.Reader
	done     bool
}

func (r *recordBatchChunkSourceReader) Read(target []byte) (int, error) {
	if r == nil || r.rows == nil || r.done {
		return 0, io.EOF
	}
	for {
		if r.current != nil && r.current.Len() > 0 {
			return r.current.Read(target)
		}
		r.current = nil
		if !r.rows.Next() {
			r.done = true
			rowErr := r.rows.Err()
			_ = r.rows.Close()
			if rowErr != nil {
				return 0, rowErr
			}
			if r.next != r.expected {
				return 0, fmt.Errorf("record batch source chunk count mismatch: expected %d, got %d", r.expected, r.next)
			}
			return 0, io.EOF
		}
		var sequence int
		var content string
		if err := r.rows.Scan(&sequence, &content); err != nil {
			return 0, err
		}
		if sequence != r.next {
			return 0, fmt.Errorf("record batch source chunk sequence mismatch: expected %d, got %d", r.next, sequence)
		}
		r.next++
		r.current = strings.NewReader(content)
	}
}

func (r *recordBatchChunkSourceReader) Close() error {
	if r == nil || r.rows == nil {
		return nil
	}
	r.done = true
	return r.rows.Close()
}
