package integration

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin integration connection delete: %w", err)
	}
	defer tx.Rollback()
	references := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_integration_webhook_subscriptions", workspaceID).Columns("id").Where(ormbuilder.Equal("connection_key", connectionKey))
	query, args, err := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, "_integration_connections", workspaceID).Where(ormbuilder.And(ormbuilder.Equal("connection_key", connectionKey), ormbuilder.NotExistsSubquery(references))).Build()
	if err != nil {
		return false, fmt.Errorf("build integration connection delete: %w", err)
	}
	result, err := tx.ExecContext(ctx, query, args...)
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
	stateDelete, stateArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, "_integration_connector_provider_states", workspaceID).Where(ormbuilder.Equal("connection_key", connectionKey)).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build connector provider state delete: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, stateDelete, stateArgs...); err != nil {
		return false, fmt.Errorf("delete connector provider states: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit integration connection delete: %w", err)
	}
	return true, nil
}
