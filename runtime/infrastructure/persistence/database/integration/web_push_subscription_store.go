package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

const webPushColumns = "id, workspace_id, user_id, endpoint_hash, endpoint, p256dh, auth_secret, status, expires_at, created_at, updated_at, revoked_at"

func (r IntegrationDeliveryStore) ListWebPushSubscriptions(ctx context.Context, workspaceID, userID string) ([]integrationmodel.WebPushSubscription, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, "SELECT "+webPushColumns+" FROM "+r.store.TableIdentifier("web_push_subscriptions")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("user_id")+" = "+r.store.Placeholder(2)+" ORDER BY "+r.store.Identifier("created_at"), workspaceID, strings.TrimSpace(userID))
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
	value, err := scanWebPush(r.db.QueryRowContext(ctx, "SELECT "+webPushColumns+" FROM "+r.store.TableIdentifier("web_push_subscriptions")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, strings.TrimSpace(id)).Scan)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("web_push_subscriptions")+" SET "+r.store.Identifier("endpoint_hash")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("endpoint")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("p256dh")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("auth_secret")+" = "+r.store.Placeholder(4)+", "+r.store.Identifier("status")+" = 'active', "+r.store.Identifier("expires_at")+" = "+r.store.Placeholder(5)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(6)+", "+r.store.Identifier("revoked_at")+" = '' WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(7)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(8)+" AND "+r.store.Identifier("user_id")+" = "+r.store.Placeholder(9), value.EndpointHash, value.Endpoint, value.P256DH, value.Auth, value.ExpiresAt, now, workspaceID, value.ID, value.UserID)
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
		_, err = r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("web_push_subscriptions")+" ("+webPushColumns+") VALUES ("+stringsJoinPlaceholders(r.store, 12)+")", value.ID, workspaceID, value.UserID, value.EndpointHash, value.Endpoint, value.P256DH, value.Auth, value.Status, value.ExpiresAt, value.CreatedAt, value.UpdatedAt, value.RevokedAt)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("web_push_subscriptions")+" SET "+r.store.Identifier("status")+" = 'revoked', "+r.store.Identifier("endpoint")+" = '', "+r.store.Identifier("p256dh")+" = '', "+r.store.Identifier("auth_secret")+" = '', "+r.store.Identifier("revoked_at")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(4)+" AND "+r.store.Identifier("user_id")+" = "+r.store.Placeholder(5), now, now, workspaceID, strings.TrimSpace(id), strings.TrimSpace(userID))
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
	_, err = r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("web_push_subscriptions")+" SET "+r.store.Identifier("status")+" = 'expired', "+r.store.Identifier("endpoint")+" = '', "+r.store.Identifier("p256dh")+" = '', "+r.store.Identifier("auth_secret")+" = '', "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(3), now, workspaceID, strings.TrimSpace(id))
	return err
}
func (r IntegrationDeliveryStore) CleanupExpiredWebPushSubscriptions(ctx context.Context, workspaceID string) (int, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("web_push_subscriptions")+" SET "+r.store.Identifier("status")+" = 'expired', "+r.store.Identifier("endpoint")+" = '', "+r.store.Identifier("p256dh")+" = '', "+r.store.Identifier("auth_secret")+" = '', "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("status")+" = 'active' AND "+r.store.Identifier("expires_at")+" <> '' AND "+r.store.Identifier("expires_at")+" <= "+r.store.Placeholder(3), now, workspaceID, now)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

type scanner func(...any) error

func scanWebPush(scan scanner) (integrationmodel.WebPushSubscription, error) {
	var value integrationmodel.WebPushSubscription
	err := scan(&value.ID, &value.WorkspaceID, &value.UserID, &value.EndpointHash, &value.Endpoint, &value.P256DH, &value.Auth, &value.Status, &value.ExpiresAt, &value.CreatedAt, &value.UpdatedAt, &value.RevokedAt)
	return value, err
}
