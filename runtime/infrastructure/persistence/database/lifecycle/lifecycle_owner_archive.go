package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (e OwnerExecutor) archiveCandidate(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, resourceID string) (bool, error) {
	var exists int
	check, checkArgs, buildErr := lifecycleArchiveExistsQuery(e.store, job.WorkspaceID, spec.table, resourceID, policy.Policy.Key)
	if buildErr != nil {
		return false, buildErr
	}
	if err := e.database().QueryRowContext(ctx, check, checkArgs...).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	predicate := ormbuilder.Predicate(ormbuilder.Equal(spec.idColumn, resourceID))
	builder := ormbuilder.NewSelectBuilder(e.store.SQLRenderer, spec.table).Projections(ormbuilder.Project(ormbuilder.Star()))
	if spec.tenantColumn != "" {
		builder = ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, spec.table, job.WorkspaceID).Projections(ormbuilder.Project(ormbuilder.Star()))
	}
	query, args, buildErr := builder.Where(predicate).Build()
	if buildErr != nil {
		return false, buildErr
	}
	rows, err := e.database().QueryContext(ctx, query, args...)
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
	check, checkArgs, buildErr := lifecycleArchiveExistsQuery(e.store, job.WorkspaceID, sourceTable, resourceID, policy.Policy.Key)
	if buildErr != nil {
		return false, buildErr
	}
	if err := e.database().QueryRowContext(ctx, check, checkArgs...).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}
	digest := sha256.Sum256(raw)
	insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(e.store.SQLRenderer, "lifecycle_archive_entries", job.WorkspaceID).Columns("id", "owner", "source_table", "resource_id", "policy_key", "policy_version", "job_id", "payload_hash", "payload_json", "archived_at").Values(requestcontext.NewRequestID(), e.owner, sourceTable, resourceID, policy.Policy.Key, policy.Policy.Version, job.ID, hex.EncodeToString(digest[:]), string(raw), time.Now().UTC().Format(time.RFC3339Nano)).Build()
	if buildErr != nil {
		return false, buildErr
	}
	_, err := e.database().ExecContext(ctx, insert, insertArgs...)
	if err != nil {
		return false, fmt.Errorf("archive %s %s: %w", sourceTable, resourceID, err)
	}
	return true, nil
}

func lifecycleArchiveExistsQuery(store *database.RuntimeStore, workspaceID, sourceTable, resourceID, policyKey string) (string, []any, error) {
	return ormbuilder.NewWorkspaceSelectBuilder(store.SQLRenderer, "lifecycle_archive_entries", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).
		Where(ormbuilder.And(ormbuilder.Equal("source_table", sourceTable), ormbuilder.Equal("resource_id", resourceID), ormbuilder.Equal("policy_key", policyKey))).Build()
}
