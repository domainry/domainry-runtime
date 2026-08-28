package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) archiveCandidate(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, resourceID string) (bool, error) {
	var exists int
	check := "SELECT COUNT(*) FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("source_table") + " = " + e.store.Placeholder(2) + " AND " + e.store.Identifier("resource_id") + " = " + e.store.Placeholder(3) + " AND " + e.store.Identifier("policy_key") + " = " + e.store.Placeholder(4)
	if err := e.database().QueryRowContext(ctx, check, job.WorkspaceID, spec.table, resourceID, policy.Policy.Key).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	where, args := e.store.Identifier(spec.idColumn)+" = "+e.store.Placeholder(1), []any{resourceID}
	if spec.tenantColumn != "" {
		where = e.store.Identifier(spec.tenantColumn) + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier(spec.idColumn) + " = " + e.store.Placeholder(2)
		args = []any{job.WorkspaceID, resourceID}
	}
	rows, err := e.database().QueryContext(ctx, "SELECT * FROM "+e.store.TableIdentifier(spec.table)+" WHERE "+where, args...)
	if err != nil {
		return false, err
	}
	columns, _ := rows.Columns()
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		return false, err
	}
	values, pointers := make([]any, len(columns)), make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	_ = rows.Scan(pointers...)
	if err := rows.Close(); err != nil {
		return false, err
	}
	payload := map[string]any{}
	for index, column := range columns {
		if bytes, ok := values[index].([]byte); ok {
			payload[column] = string(bytes)
		} else {
			payload[column] = values[index]
		}
	}
	raw, _ := json.Marshal(payload)
	return e.archivePayload(ctx, job, policy, spec.table, resourceID, raw)
}

func (e OwnerExecutor) archivePayload(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, sourceTable, resourceID string, raw []byte) (bool, error) {
	var exists int
	check := "SELECT COUNT(*) FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("source_table") + " = " + e.store.Placeholder(2) + " AND " + e.store.Identifier("resource_id") + " = " + e.store.Placeholder(3) + " AND " + e.store.Identifier("policy_key") + " = " + e.store.Placeholder(4)
	if err := e.database().QueryRowContext(ctx, check, job.WorkspaceID, sourceTable, resourceID, policy.Policy.Key).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	digest := sha256.Sum256(raw)
	columnsToInsert := []string{"id", "workspace_id", "owner", "source_table", "resource_id", "policy_key", "policy_version", "job_id", "payload_hash", "payload_json", "archived_at"}
	_, err := e.database().ExecContext(ctx, e.store.InsertStatement("lifecycle_archive_entries", columnsToInsert), requestcontext.NewRequestID(), job.WorkspaceID, e.owner, sourceTable, resourceID, policy.Policy.Key, policy.Policy.Version, job.ID, hex.EncodeToString(digest[:]), string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("archive %s %s: %w", sourceTable, resourceID, err)
	}
	return true, nil
}
