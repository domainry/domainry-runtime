package integration

import (
	"context"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
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
	statement, args, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, "_integration_webhook_subscriptions", workspaceID).
		Where(ormbuilder.Equal("subscription_key", subscriptionKey)).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build integration webhook subscription delete: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, fmt.Errorf("delete integration webhook subscription: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read deleted integration webhook subscription count: %w", err)
	}
	return count == 1, nil
}
