package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func webPushSubscriptionColumns() []string {
	return []string{"id", "workspace_id", "user_id", "endpoint_hash", "endpoint", "p256dh", "auth_secret", "status", "expires_at", "created_at", "updated_at", "revoked_at"}
}

func (r IntegrationDeliveryStore) ListWebPushSubscriptions(ctx context.Context, workspaceID, userID string) ([]integrationmodel.WebPushSubscription, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
		Columns(webPushSubscriptionColumns()...).Where(ormbuilder.Equal("user_id", strings.TrimSpace(userID))).OrderBy(ormbuilder.Ascending("created_at")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build web push subscription list: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list web push subscriptions: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.WebPushSubscription{}
	for rows.Next() {
		value, scanErr := scanWebPush(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (r IntegrationDeliveryStore) GetWebPushSubscription(ctx context.Context, workspaceID, id string) (integrationmodel.WebPushSubscription, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, false, err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
		Columns(webPushSubscriptionColumns()...).Where(ormbuilder.Equal("id", strings.TrimSpace(id))).Limit(1).Build()
	if buildErr != nil {
		return integrationmodel.WebPushSubscription{}, false, fmt.Errorf("build web push subscription read: %w", buildErr)
	}
	value, err := scanWebPush(r.db.QueryRowContext(ctx, query, args...).Scan)
	if err == sql.ErrNoRows {
		return integrationmodel.WebPushSubscription{}, false, nil
	}
	return value, err == nil, err
}
func (r IntegrationDeliveryStore) UpsertWebPushSubscription(ctx context.Context, workspaceID string, value integrationmodel.WebPushSubscription) (integrationmodel.WebPushSubscription, error) {
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	value.WorkspaceID = workspaceID
	value.Status = "active"
	existing, found, err := r.GetWebPushSubscription(ctx, workspaceID, value.ID)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	if found && existing.UserID != value.UserID {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("web push subscription not found")
	}
	if found && existing.Status == "active" && existing.EndpointHash == value.EndpointHash && existing.Endpoint == value.Endpoint && existing.P256DH == value.P256DH && existing.Auth == value.Auth && existing.ExpiresAt == value.ExpiresAt {
		return existing, nil
	}
	value.UpdatedAt = now
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
		Set("endpoint_hash", value.EndpointHash).Set("endpoint", value.Endpoint).Set("p256dh", value.P256DH).Set("auth_secret", value.Auth).
		Set("status", "active").Set("expires_at", value.ExpiresAt).Set("updated_at", now).Set("revoked_at", "").
		Where(ormbuilder.And(ormbuilder.Equal("id", value.ID), ormbuilder.Equal("user_id", value.UserID))).Build()
	if buildErr != nil {
		return value, fmt.Errorf("build web push subscription update: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return value, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		if _, found, getErr := r.GetWebPushSubscription(ctx, workspaceID, value.ID); getErr != nil {
			return value, getErr
		} else if found {
			return value, fmt.Errorf("web push subscription not found")
		}
		statement, args, buildErr = ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
			Columns("id", "user_id", "endpoint_hash", "endpoint", "p256dh", "auth_secret", "status", "expires_at", "created_at", "updated_at", "revoked_at").
			Values(value.ID, value.UserID, value.EndpointHash, value.Endpoint, value.P256DH, value.Auth, value.Status, value.ExpiresAt, value.CreatedAt, value.UpdatedAt, value.RevokedAt).Build()
		if buildErr != nil {
			return value, fmt.Errorf("build web push subscription insert: %w", buildErr)
		}
		_, err = r.db.ExecContext(ctx, statement, args...)
	}
	if err != nil {
		return value, fmt.Errorf("upsert web push subscription: %w", err)
	}
	return value, nil
}
func (r IntegrationDeliveryStore) RevokeWebPushSubscription(ctx context.Context, workspaceID, id, userID string) (integrationmodel.WebPushSubscription, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	id, userID = strings.TrimSpace(id), strings.TrimSpace(userID)
	existing, found, err := r.GetWebPushSubscription(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	if !found || existing.UserID != userID {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("web push subscription not found")
	}
	if existing.Status != "active" {
		return existing, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
		Set("status", "revoked").Set("endpoint", "").Set("p256dh", "").Set("auth_secret", "").Set("revoked_at", now).Set("updated_at", now).
		Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.Equal("user_id", userID))).Build()
	if buildErr != nil {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("build web push subscription revoke: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("web push subscription not found")
	}
	value, _, err := r.GetWebPushSubscription(ctx, workspaceID, id)
	return value, err
}
func (r IntegrationDeliveryStore) ExpireWebPushSubscription(ctx context.Context, workspaceID, id string) error {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	statement, args, buildErr := webPushExpirationUpdate(r, workspaceID, now, ormbuilder.Equal("id", strings.TrimSpace(id)))
	if buildErr != nil {
		return fmt.Errorf("build web push subscription expiration: %w", buildErr)
	}
	_, err = r.db.ExecContext(ctx, statement, args...)
	return err
}
func (r IntegrationDeliveryStore) CleanupExpiredWebPushSubscriptions(ctx context.Context, workspaceID string) (int, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	statement, args, buildErr := webPushExpirationUpdate(r, workspaceID, now, ormbuilder.And(
		ormbuilder.Equal("status", "active"), ormbuilder.NotEqual("expires_at", ""), ormbuilder.LessThanOrEqual("expires_at", now),
	))
	if buildErr != nil {
		return 0, fmt.Errorf("build expired web push subscription cleanup: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func webPushExpirationUpdate(r IntegrationDeliveryStore, workspaceID, now string, predicate ormbuilder.Predicate) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "web_push_subscriptions", workspaceID).
		Set("status", "expired").Set("endpoint", "").Set("p256dh", "").Set("auth_secret", "").Set("updated_at", now).
		Where(predicate).Build()
}

type scanner func(...any) error

func scanWebPush(scan scanner) (integrationmodel.WebPushSubscription, error) {
	var value integrationmodel.WebPushSubscription
	err := scan(&value.ID, &value.WorkspaceID, &value.UserID, &value.EndpointHash, &value.Endpoint, &value.P256DH, &value.Auth, &value.Status, &value.ExpiresAt, &value.CreatedAt, &value.UpdatedAt, &value.RevokedAt)
	return value, err
}
