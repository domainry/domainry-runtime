package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type ReportOwnerExecutor struct {
	store      *database.RuntimeStore
	db         *sql.DB
	relational OwnerExecutor
}

func (e ReportOwnerExecutor) database() *sql.DB {
	if e.db != nil {
		return e.db
	}
	return e.store.DB()
}

func (e ReportOwnerExecutor) Owner(context.Context) string { return "report" }

func (e ReportOwnerExecutor) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	result := lifecyclecontract.CleanupPreview{}
	if len(e.relational.policySpecs(policy.Policy.Key)) > 0 {
		preview, err := e.relational.Preview(ctx, workspaceID, policy, now)
		if err != nil {
			return result, err
		}
		result = preview
	}
	states, err := e.reportStateCandidates(ctx, workspaceID, policy.Policy, now, 0)
	if err != nil {
		return result, err
	}
	result.Rows += int64(len(states))
	result.Bytes += int64(len(states)) * 1024
	for _, state := range states {
		updated := time.Unix(0, state.updatedAt).UTC()
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
	}
	return result, nil
}

func (e ReportOwnerExecutor) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	result := lifecyclemodel.CleanupBatchResult{Done: true}
	remaining := batchSize
	if len(e.relational.policySpecs(policy.Policy.Key)) > 0 {
		relational, err := e.relational.ProcessBatch(ctx, job, policy, holds, remaining)
		if err != nil {
			return relational, err
		}
		mergeCleanupBatchResult(&result, relational)
		remaining -= int(relational.Scanned)
		if remaining <= 0 {
			result.Done = false
			return result, nil
		}
	}
	states, err := e.reportStateCandidates(ctx, job.WorkspaceID, policy.Policy, job.UpdatedAt, remaining)
	if err != nil {
		return result, err
	}
	if len(states) >= remaining {
		result.Done = false
	}
	archiver := OwnerExecutor{store: e.store, db: e.database(), owner: "report"}
	for _, state := range states {
		resourceID := state.kind + ":" + state.stateKey
		result.Scanned++
		result.Checkpoint = "agent_runtime_state:" + resourceID
		updated := time.Unix(0, state.updatedAt).UTC()
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
		if lifecycleHeld(holds, "report", "agent_runtime_state", resourceID, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.Operation == lifecyclemodel.OperationPurge {
			referenced, referenceErr := e.reportStateReferenced(ctx, job.WorkspaceID, state.kind, state.stateKey)
			if referenceErr != nil {
				return result, referenceErr
			}
			if referenced {
				result.Skipped++
				continue
			}
		}
		if job.DryRun {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"workspace_id": job.WorkspaceID, "kind": state.kind, "state_key": state.stateKey, "user_id": state.userID, "role_key": state.roleKey, "payload_json": json.RawMessage(state.payload), "updated_at": state.updatedAt})
		archived, archiveErr := archiver.archivePayload(ctx, job, policy, "agent_runtime_state", resourceID, raw)
		if archiveErr != nil {
			result.Failed++
			return result, archiveErr
		}
		if archived {
			result.Archived++
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		query, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, "agent_runtime_state", job.WorkspaceID).Where(ormbuilder.And(ormbuilder.Equal("kind", state.kind), ormbuilder.Equal("state_key", state.stateKey))).Build()
		if buildErr != nil {
			result.Failed++
			return result, buildErr
		}
		deleted, deleteErr := e.database().ExecContext(ctx, query, args...)
		if deleteErr != nil {
			result.Failed++
			return result, deleteErr
		}
		count, rowsErr := deleted.RowsAffected()
		if rowsErr != nil {
			result.Failed++
			return result, rowsErr
		}
		result.Purged += count
	}
	return result, nil
}

func (e ReportOwnerExecutor) reportStateCandidates(ctx context.Context, workspaceID string, policy lifecyclemodel.RetentionPolicy, now time.Time, limit int) ([]agentLifecycleCandidate, error) {
	result := []agentLifecycleCandidate{}
	allKinds := []struct {
		policyKey string
		kind      string
		retention time.Duration
	}{{"report.download.v1", "report_download_task", policy.DefaultRetention}, {"report.export.v1", "report_export_audit", reportRetention(policy, "succeeded", policy.DefaultRetention)}, {"report.export.v1", "report_query_run", reportRetention(policy, "succeeded", policy.DefaultRetention)}}
	for _, item := range allKinds {
		if item.policyKey != policy.Key {
			continue
		}
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "agent_runtime_state", workspaceID).Columns("state_key", "user_id", "role_key", "payload_json", "updated_at").Where(ormbuilder.And(ormbuilder.Equal("kind", item.kind), ormbuilder.LessThanOrEqual("updated_at", now.Add(-item.retention).UTC().UnixNano()))).OrderBy(ormbuilder.Ascending("updated_at"), ormbuilder.Ascending("state_key")).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		rows, err := e.database().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var state agentLifecycleCandidate
			var payload []byte
			state.kind = item.kind
			if err := rows.Scan(&state.stateKey, &state.userID, &state.roleKey, &payload, &state.updatedAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			state.payload = string(payload)
			result = append(result, state)
			if limit > 0 && len(result) >= limit {
				_ = rows.Close()
				return result, nil
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return result, nil
}

func (e ReportOwnerExecutor) reportStateReferenced(ctx context.Context, workspaceID, kind, stateKey string) (bool, error) {
	childKind := ""
	switch kind {
	case "report_export_audit":
		childKind = "report_download_task"
	case "report_query_run":
		childKind = "report_export_audit"
	}
	if childKind == "" {
		return false, nil
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "agent_runtime_state", workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.Equal("kind", childKind), ormbuilder.Equal("state_key", stateKey))).Build()
	if buildErr != nil {
		return false, buildErr
	}
	var count int
	if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func reportRetention(policy lifecyclemodel.RetentionPolicy, group string, fallback time.Duration) time.Duration {
	if retention := policy.StatusRetention[group]; retention > 0 {
		return retention
	}
	if fallback > 0 {
		return fallback
	}
	return policy.DefaultRetention
}

func mergeCleanupBatchResult(target *lifecyclemodel.CleanupBatchResult, source lifecyclemodel.CleanupBatchResult) {
	target.Scanned += source.Scanned
	target.Archived += source.Archived
	target.Purged += source.Purged
	target.Skipped += source.Skipped
	target.Failed += source.Failed
	if source.Checkpoint != "" {
		target.Checkpoint = source.Checkpoint
	}
	if !source.OldestEligible.IsZero() && (target.OldestEligible.IsZero() || source.OldestEligible.Before(target.OldestEligible)) {
		target.OldestEligible = source.OldestEligible
	}
	if !source.Done {
		target.Done = false
	}
}

var _ lifecyclecontract.OwnerLifecycleExecutor = ReportOwnerExecutor{}
