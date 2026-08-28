package integration

import (
	"context"
	"fmt"
	"strings"
)

func (r IntegrationConfigStore) DeleteWebhookSubscription(ctx context.Context, workspaceID, subscriptionKey string) (bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	subscriptionKey = strings.TrimSpace(subscriptionKey)
	if subscriptionKey == "" {
		return false, fmt.Errorf("integration webhook subscription key is required")
	}
	s := r.store
	result, err := r.db.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier("integration_webhook_subscriptions")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("subscription_key")+" = "+s.Placeholder(2), workspaceID, subscriptionKey)
	if err != nil {
		return false, fmt.Errorf("delete integration webhook subscription: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read deleted integration webhook subscription count: %w", err)
	}
	return count == 1, nil
}
