package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

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
