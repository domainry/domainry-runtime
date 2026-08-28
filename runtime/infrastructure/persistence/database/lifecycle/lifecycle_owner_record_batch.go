package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) archiveRecordBatchChunks(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, jobID string, purge bool) (int64, int64, error) {
	query := "SELECT " + e.store.Identifier("sequence_no") + ", " + e.store.Identifier("content") + ", " + e.store.Identifier("created_at") + " FROM " + e.store.TableIdentifier("record_batch_job_chunks") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("job_id") + " = " + e.store.Placeholder(2) + " ORDER BY " + e.store.Identifier("sequence_no")
	rows, err := e.database().QueryContext(ctx, query, job.WorkspaceID, jobID)
	if err != nil {
		return 0, 0, err
	}
	type chunk struct {
		sequence  int64
		content   string
		createdAt string
	}
	chunks := []chunk{}
	for rows.Next() {
		var item chunk
		if err := rows.Scan(&item.sequence, &item.content, &item.createdAt); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		chunks = append(chunks, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	_ = rows.Close()
	var archived int64
	for _, item := range chunks {
		resourceID := fmt.Sprintf("%s:%d", jobID, item.sequence)
		raw, _ := json.Marshal(map[string]any{"workspace_id": job.WorkspaceID, "job_id": jobID, "sequence_no": item.sequence, "content": item.content, "created_at": item.createdAt})
		created, err := e.archivePayload(ctx, job, policy, "record_batch_job_chunks", resourceID, raw)
		if err != nil {
			return archived, 0, err
		}
		if created {
			archived++
		}
	}
	if !purge || len(chunks) == 0 {
		return archived, 0, nil
	}
	result, err := e.database().ExecContext(ctx, "DELETE FROM "+e.store.TableIdentifier("record_batch_job_chunks")+" WHERE "+e.store.Identifier("workspace_id")+" = "+e.store.Placeholder(1)+" AND "+e.store.Identifier("job_id")+" = "+e.store.Placeholder(2), job.WorkspaceID, jobID)
	if err != nil {
		return archived, 0, err
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return archived, 0, err
	}
	return archived, purged, nil
}
