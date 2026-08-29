package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

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
