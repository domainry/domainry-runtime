package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const connectorProviderStateMaxBytes = 1 << 20

func (r IntegrationConfigStore) ListConnectorProviderConnections(ctx context.Context, scope principalmodel.SystemScope) ([]integrationmodel.IntegrationConnection, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	statement, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "integration_connections").Columns(
		"connection_key", "workspace_id", "connector_key", "provider_key", "name", "status", "config_json", "secret_refs_json", "created_by", "created_at", "updated_at",
	).OrderBy(ormbuilder.Ascending("workspace_id"), ormbuilder.Ascending("connection_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build connector provider connection query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationConnection{}
	for rows.Next() {
		value, err := scanIntegrationConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (r IntegrationConfigStore) SyncConnectorProviderTasks(ctx context.Context, connection integrationmodel.IntegrationConnection, tasks []integrationrepository.ConnectorProviderTask, now string) error {
	selected := map[string]integrationrepository.ConnectorProviderTask{}
	for _, task := range tasks {
		key := strings.TrimSpace(task.Key)
		if key == "" || task.StateVersion <= 0 || selected[key].Key != "" || (len(task.InitialState) != 0 && (!json.Valid(task.InitialState) || len(task.InitialState) > connectorProviderStateMaxBytes)) {
			return fmt.Errorf("invalid or duplicate connector provider task %q", key)
		}
		selected[key] = task
	}
	tx, err := r.db.BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+r.store.Identifier("task_key")+","+r.store.Identifier("state_version")+" FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND "+r.store.Identifier("connection_key")+"="+r.store.Placeholder(2), connection.WorkspaceID, connection.Key)
	if err != nil {
		return err
	}
	existing := map[string]int{}
	for rows.Next() {
		var key string
		var version int
		if err := rows.Scan(&key, &version); err != nil {
			_ = rows.Close()
			return err
		}
		existing[key] = version
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for key := range existing {
		if _, keep := selected[key]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND "+r.store.Identifier("connection_key")+"="+r.store.Placeholder(2)+" AND "+r.store.Identifier("task_key")+"="+r.store.Placeholder(3), connection.WorkspaceID, connection.Key, key); err != nil {
			return err
		}
	}
	for key, task := range selected {
		if version, found := existing[key]; found && version != task.StateVersion {
			return fmt.Errorf("connector provider task %s state version changed from %d to %d without migration", key, version, task.StateVersion)
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND "+r.store.Identifier("connection_key")+"="+r.store.Placeholder(2)+" AND "+r.store.Identifier("task_key")+"="+r.store.Placeholder(3), connection.WorkspaceID, connection.Key, key).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		payload := task.InitialState
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		columns := []string{"id", "workspace_id", "connector_key", "provider_key", "connection_key", "task_key", "state_version", "payload_json", "status", "due_at", "last_error_code", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token", "updated_at"}
		values := []any{"connector_provider_state:" + connection.WorkspaceID + ":" + connection.ConnectorKey + ":" + connection.ProviderKey + ":" + connection.Key + ":" + key, connection.WorkspaceID, connection.ConnectorKey, connection.ProviderKey, connection.Key, key, task.StateVersion, string(payload), "ready", now, "", 0, "", "", 0, now}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("connector_provider_states")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(values))+")", values...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r IntegrationConfigStore) ListDueConnectorProviderStates(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.ConnectorProviderStateCandidate, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	stateColumns := []string{"workspace_id", "connector_key", "provider_key", "connection_key", "task_key", "state_version", "payload_json", "status", "due_at", "last_error_code", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token", "updated_at"}
	connectionColumns := []string{"name", "status", "config_json", "secret_refs_json", "created_by", "created_at", "updated_at"}
	projections := make([]ormbuilder.Projection, 0, len(stateColumns)+len(connectionColumns))
	for _, column := range stateColumns {
		projections = append(projections, ormbuilder.Project(ormbuilder.QualifiedColumn("s", column)))
	}
	for _, column := range connectionColumns {
		projections = append(projections, ormbuilder.Project(ormbuilder.QualifiedColumn("c", column)))
	}
	statement, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "connector_provider_states").Alias("s").Projections(projections...).Join(
		ormbuilder.InnerJoin("integration_connections", "c", ormbuilder.And(
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("c", "workspace_id"), ormbuilder.QualifiedColumn("s", "workspace_id")),
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("c", "connection_key"), ormbuilder.QualifiedColumn("s", "connection_key")),
		)),
	).Where(ormbuilder.And(
		ormbuilder.EqualValue(ormbuilder.QualifiedColumn("c", "status"), "active"),
		ormbuilder.Or(
			ormbuilder.And(ormbuilder.EqualValue(ormbuilder.QualifiedColumn("s", "status"), "running"), ormbuilder.LessThanOrEqualValue(ormbuilder.QualifiedColumn("s", "lease_expires_at"), now)),
			ormbuilder.And(ormbuilder.InExpression(ormbuilder.QualifiedColumn("s", "status"), "ready", "retry"), ormbuilder.Or(ormbuilder.EqualValue(ormbuilder.QualifiedColumn("s", "due_at"), ""), ormbuilder.LessThanOrEqualValue(ormbuilder.QualifiedColumn("s", "due_at"), now))),
		),
	)).OrderBy(ormbuilder.AscendingExpression(ormbuilder.QualifiedColumn("s", "due_at"))).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build runnable connector provider state query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	out := []integrationmodel.ConnectorProviderStateCandidate{}
	for rows.Next() {
		var candidate integrationmodel.ConnectorProviderStateCandidate
		var payload, config, secrets string
		s := &candidate.State
		c := &candidate.Connection
		if err := rows.Scan(&s.WorkspaceID, &s.ConnectorKey, &s.ProviderKey, &s.ConnectionKey, &s.TaskKey, &s.StateVersion, &payload, &s.Status, &s.DueAt, &s.LastErrorCode, &s.AttemptCount, &s.LeaseOwner, &s.LeaseExpiresAt, &s.FencingToken, &s.UpdatedAt, &c.Name, &c.Status, &config, &secrets, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		s.Payload = json.RawMessage(payload)
		c.WorkspaceID, c.Key, c.ConnectorKey, c.ProviderKey = s.WorkspaceID, s.ConnectionKey, s.ConnectorKey, s.ProviderKey
		if err := json.Unmarshal([]byte(config), &c.Config); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(secrets), &c.SecretRefs); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range out {
		related, err := r.connectorProviderRelatedStates(ctx, out[index].State)
		if err != nil {
			return nil, err
		}
		out[index].RelatedStates = related
	}
	return out, nil
}

func (r IntegrationConfigStore) connectorProviderRelatedStates(ctx context.Context, state integrationmodel.ConnectorProviderState) (map[string]integrationmodel.ConnectorProviderState, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+stringsJoinIdentifiers(r.store, "task_key", "state_version", "payload_json", "status", "due_at", "last_error_code", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token", "updated_at")+" FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND "+r.store.Identifier("connection_key")+"="+r.store.Placeholder(2), state.WorkspaceID, state.ConnectionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]integrationmodel.ConnectorProviderState{}
	for rows.Next() {
		v := state
		var payload string
		if err := rows.Scan(&v.TaskKey, &v.StateVersion, &payload, &v.Status, &v.DueAt, &v.LastErrorCode, &v.AttemptCount, &v.LeaseOwner, &v.LeaseExpiresAt, &v.FencingToken, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Payload = json.RawMessage(payload)
		out[v.TaskKey] = v
	}
	return out, rows.Err()
}

func (r IntegrationConfigStore) ClaimConnectorProviderState(ctx context.Context, workspaceID, connectionKey, taskKey, owner, now, expires string) (integrationmodel.ConnectorProviderState, bool, error) {
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("connector_provider_states")+" SET status='running',lease_owner="+r.store.Placeholder(1)+",lease_expires_at="+r.store.Placeholder(2)+",fencing_token=fencing_token+1,updated_at="+r.store.Placeholder(3)+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(4)+" AND connection_key="+r.store.Placeholder(5)+" AND task_key="+r.store.Placeholder(6)+" AND ((status IN ('ready','retry') AND (due_at='' OR due_at<="+r.store.Placeholder(7)+")) OR (status='running' AND lease_expires_at<="+r.store.Placeholder(8)+"))", owner, expires, now, workspaceID, connectionKey, taskKey, now, now)
	if err != nil {
		return integrationmodel.ConnectorProviderState{}, false, err
	}
	count, _ := result.RowsAffected()
	state, found, err := r.getConnectorProviderState(ctx, workspaceID, connectionKey, taskKey)
	return state, found && count == 1, err
}
func (r IntegrationConfigStore) CompleteConnectorProviderState(ctx context.Context, state integrationmodel.ConnectorProviderState, payload json.RawMessage, dueAt, now string) (integrationmodel.ConnectorProviderState, error) {
	if !json.Valid(payload) || len(payload) > connectorProviderStateMaxBytes {
		return state, fmt.Errorf("connector provider state payload is invalid or exceeds %d bytes", connectorProviderStateMaxBytes)
	}
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("connector_provider_states")+" SET payload_json="+r.store.Placeholder(1)+",state_version="+r.store.Placeholder(2)+",status='ready',due_at="+r.store.Placeholder(3)+",last_error_code='',attempt_count=0,lease_owner='',lease_expires_at='',updated_at="+r.store.Placeholder(4)+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(5)+" AND connection_key="+r.store.Placeholder(6)+" AND task_key="+r.store.Placeholder(7)+" AND status='running' AND lease_owner="+r.store.Placeholder(8)+" AND fencing_token="+r.store.Placeholder(9), string(payload), state.StateVersion, dueAt, now, state.WorkspaceID, state.ConnectionKey, state.TaskKey, state.LeaseOwner, state.FencingToken)
	if err != nil {
		return state, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return state, fmt.Errorf("backend.integration.connector_provider_state.stale_lease")
	}
	value, _, err := r.getConnectorProviderState(ctx, state.WorkspaceID, state.ConnectionKey, state.TaskKey)
	return value, err
}
func (r IntegrationConfigStore) FailConnectorProviderState(ctx context.Context, state integrationmodel.ConnectorProviderState, code, dueAt, now string) (integrationmodel.ConnectorProviderState, error) {
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("connector_provider_states")+" SET status='retry',due_at="+r.store.Placeholder(1)+",last_error_code="+r.store.Placeholder(2)+",attempt_count=attempt_count+1,lease_owner='',lease_expires_at='',updated_at="+r.store.Placeholder(3)+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(4)+" AND connection_key="+r.store.Placeholder(5)+" AND task_key="+r.store.Placeholder(6)+" AND status='running' AND lease_owner="+r.store.Placeholder(7)+" AND fencing_token="+r.store.Placeholder(8), dueAt, code, now, state.WorkspaceID, state.ConnectionKey, state.TaskKey, state.LeaseOwner, state.FencingToken)
	if err != nil {
		return state, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return state, fmt.Errorf("backend.integration.connector_provider_state.stale_lease")
	}
	value, _, err := r.getConnectorProviderState(ctx, state.WorkspaceID, state.ConnectionKey, state.TaskKey)
	return value, err
}
func (r IntegrationConfigStore) WakeConnectorProviderState(ctx context.Context, workspaceID, connectionKey, taskKey, now string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("connector_provider_states")+" SET due_at="+r.store.Placeholder(1)+",updated_at="+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(3)+" AND connection_key="+r.store.Placeholder(4)+" AND task_key="+r.store.Placeholder(5)+" AND status<>'running'", now, now, workspaceID, connectionKey, taskKey)
	return err
}
func (r IntegrationConfigStore) DeleteConnectorProviderStates(ctx context.Context, workspaceID, connectionKey string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND connection_key="+r.store.Placeholder(2), workspaceID, connectionKey)
	return err
}
func (r IntegrationConfigStore) getConnectorProviderState(ctx context.Context, workspaceID, connectionKey, taskKey string) (integrationmodel.ConnectorProviderState, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+stringsJoinIdentifiers(r.store, "workspace_id", "connector_key", "provider_key", "connection_key", "task_key", "state_version", "payload_json", "status", "due_at", "last_error_code", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token", "updated_at")+" FROM "+r.store.TableIdentifier("connector_provider_states")+" WHERE "+r.store.Identifier("workspace_id")+"="+r.store.Placeholder(1)+" AND connection_key="+r.store.Placeholder(2)+" AND task_key="+r.store.Placeholder(3), workspaceID, connectionKey, taskKey)
	var s integrationmodel.ConnectorProviderState
	var payload string
	if err := row.Scan(&s.WorkspaceID, &s.ConnectorKey, &s.ProviderKey, &s.ConnectionKey, &s.TaskKey, &s.StateVersion, &payload, &s.Status, &s.DueAt, &s.LastErrorCode, &s.AttemptCount, &s.LeaseOwner, &s.LeaseExpiresAt, &s.FencingToken, &s.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return s, false, nil
		}
		return s, false, err
	}
	s.Payload = json.RawMessage(payload)
	return s, true, nil
}

var _ integrationrepository.ConnectorProviderStateRepository = IntegrationConfigStore{}
