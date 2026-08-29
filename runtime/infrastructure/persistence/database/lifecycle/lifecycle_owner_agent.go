package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AgentOwnerExecutor struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func (e AgentOwnerExecutor) database() *sql.DB {
	if e.db != nil {
		return e.db
	}
	return e.store.DB()
}

type agentLifecycleCandidate struct {
	kind, stateKey, userID, roleKey, payload string
	updatedAt                                int64
}

func (e AgentOwnerExecutor) Owner(context.Context) string { return "agent" }

func (e AgentOwnerExecutor) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	cutoff := now.Add(-policy.Policy.DefaultRetention)
	candidates, err := e.candidates(ctx, workspaceID, cutoff, 0)
	if err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	preview := lifecyclecontract.CleanupPreview{Rows: int64(len(candidates)), Bytes: int64(len(candidates)) * 1024}
	for _, candidate := range candidates {
		updated := time.Unix(0, candidate.updatedAt).UTC()
		if preview.OldestEligible.IsZero() || updated.Before(preview.OldestEligible) {
			preview.OldestEligible = updated
		}
	}
	return preview, nil
}

func (e AgentOwnerExecutor) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	cutoff := job.UpdatedAt.Add(-policy.Policy.DefaultRetention)
	candidates, err := e.candidates(ctx, job.WorkspaceID, cutoff, batchSize)
	if err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	result := lifecyclemodel.CleanupBatchResult{Done: len(candidates) < batchSize}
	archiver := OwnerExecutor{store: e.store, db: e.database(), owner: "agent"}
	for _, candidate := range candidates {
		resourceID := candidate.kind + ":" + candidate.stateKey
		result.Scanned++
		result.Checkpoint = "agent_runtime_state:" + resourceID
		updated := time.Unix(0, candidate.updatedAt).UTC()
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
		if lifecycleHeld(holds, "agent", "agent_runtime_state", resourceID, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.DryRun {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"workspace_id": job.WorkspaceID, "kind": candidate.kind, "state_key": candidate.stateKey, "user_id": candidate.userID, "role_key": candidate.roleKey, "payload_json": json.RawMessage(candidate.payload), "updated_at": candidate.updatedAt})
		archived, err := archiver.archivePayload(ctx, job, policy, "agent_runtime_state", resourceID, raw)
		if err != nil {
			result.Failed++
			return result, err
		}
		if archived {
			result.Archived++
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		archive := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "lifecycle_archive_entries", job.WorkspaceID).Columns("id").Where(ormbuilder.And(ormbuilder.Equal("source_table", "agent_runtime_state"), ormbuilder.Equal("resource_id", resourceID)))
		query, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, "agent_runtime_state", job.WorkspaceID).Where(ormbuilder.And(ormbuilder.Equal("kind", candidate.kind), ormbuilder.Equal("state_key", candidate.stateKey), ormbuilder.ExistsSubquery(archive))).Build()
		if buildErr != nil {
			result.Failed++
			return result, buildErr
		}
		deleted, err := e.database().ExecContext(ctx, query, args...)
		if err != nil {
			result.Failed++
			return result, err
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

func (e AgentOwnerExecutor) candidates(ctx context.Context, workspaceID string, cutoff time.Time, limit int) ([]agentLifecycleCandidate, error) {
	builder := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "agent_runtime_state", workspaceID).Columns("kind", "state_key", "user_id", "role_key", "payload_json", "updated_at").Where(ormbuilder.And(ormbuilder.In("kind", "session", "proposal"), ormbuilder.LessThanOrEqual("updated_at", cutoff.UTC().UnixNano()))).OrderBy(ormbuilder.Ascending("updated_at"), ormbuilder.Ascending("kind"), ormbuilder.Ascending("state_key"))
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := e.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []agentLifecycleCandidate{}
	for rows.Next() {
		var candidate agentLifecycleCandidate
		var payload []byte
		if err := rows.Scan(&candidate.kind, &candidate.stateKey, &candidate.userID, &candidate.roleKey, &payload, &candidate.updatedAt); err != nil {
			return nil, err
		}
		candidate.payload = string(payload)
		if !agentLifecycleEligible(candidate.kind, payload) {
			continue
		}
		result = append(result, candidate)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, rows.Err()
}

func agentLifecycleEligible(kind string, payload []byte) bool {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	switch kind {
	case "session":
		archived, _ := value["archived"].(bool)
		return archived
	case "proposal":
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["status"])))
		return status != "" && status != "draft" && status != "pending" && status != "open"
	default:
		return false
	}
}
