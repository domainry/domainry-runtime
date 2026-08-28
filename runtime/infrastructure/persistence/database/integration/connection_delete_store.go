package integration

import (
	"context"
	"fmt"
	"strings"
)

func (r IntegrationConfigStore) DeleteConnection(ctx context.Context, workspaceID, connectionKey string) (bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	connectionKey = strings.TrimSpace(connectionKey)
	if connectionKey == "" {
		return false, fmt.Errorf("integration connection key is required")
	}
	s := r.store
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin integration connection delete: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier("integration_connections")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(2)+" AND NOT EXISTS (SELECT 1 FROM "+s.TableIdentifier("integration_webhook_subscriptions")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(3)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(4)+")", workspaceID, connectionKey, workspaceID, connectionKey)
	if err != nil {
		return false, fmt.Errorf("delete integration connection: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read deleted integration connection count: %w", err)
	}
	if count != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier("connector_provider_states")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(2), workspaceID, connectionKey); err != nil {
		return false, fmt.Errorf("delete connector provider states: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit integration connection delete: %w", err)
	}
	return true, nil
}
