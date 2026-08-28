package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
		query := "DELETE FROM " + e.store.TableIdentifier("agent_runtime_state") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("kind") + " = " + e.store.Placeholder(2) + " AND " + e.store.Identifier("state_key") + " = " + e.store.Placeholder(3) + " AND EXISTS (SELECT 1 FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(4) + " AND " + e.store.Identifier("source_table") + " = 'agent_runtime_state' AND " + e.store.Identifier("resource_id") + " = " + e.store.Placeholder(5) + ")"
		deleted, err := e.database().ExecContext(ctx, query, job.WorkspaceID, candidate.kind, candidate.stateKey, job.WorkspaceID, resourceID)
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
	query := "SELECT " + strings.Join(database.QuotedColumns(e.store, []string{"kind", "state_key", "user_id", "role_key", "payload_json", "updated_at"}), ", ") + " FROM " + e.store.TableIdentifier("agent_runtime_state") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("kind") + " IN ('session', 'proposal') AND " + e.store.Identifier("updated_at") + " <= " + e.store.Placeholder(2) + " ORDER BY " + e.store.Identifier("updated_at") + ", " + e.store.Identifier("kind") + ", " + e.store.Identifier("state_key")
	rows, err := e.database().QueryContext(ctx, query, workspaceID, cutoff.UTC().UnixNano())
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
