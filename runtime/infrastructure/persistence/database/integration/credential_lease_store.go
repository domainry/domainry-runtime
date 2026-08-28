package integration

import (
	"context"
	"fmt"
	"strings"
)

func (r IntegrationConfigStore) TryAcquireCredentialRefreshLease(ctx context.Context, workspaceID, connectionKey, owner, now, expiresAt string) (bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	connectionKey, owner = strings.TrimSpace(connectionKey), strings.TrimSpace(owner)
	if connectionKey == "" || owner == "" || strings.TrimSpace(now) == "" || strings.TrimSpace(expiresAt) == "" {
		return false, fmt.Errorf("credential refresh lease identity and timestamps are required")
	}
	s := r.store
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_credential_refresh_leases")+" SET "+s.Identifier("lease_owner")+" = "+s.Placeholder(1)+", "+s.Identifier("lease_expires_at")+" = "+s.Placeholder(2)+", "+s.Identifier("updated_at")+" = "+s.Placeholder(3)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(4)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(5)+" AND ("+s.Identifier("lease_owner")+" = "+s.Placeholder(6)+" OR "+s.Identifier("lease_expires_at")+" <= "+s.Placeholder(7)+")", owner, expiresAt, now, workspaceID, connectionKey, owner, now)
	if err != nil {
		return false, fmt.Errorf("update credential refresh lease: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read updated credential refresh lease count: %w", err)
	}
	if count > 0 {
		return true, nil
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO "+s.TableIdentifier("integration_credential_refresh_leases")+" ("+stringsJoinIdentifiers(s, "id", "workspace_id", "connection_key", "lease_owner", "lease_expires_at", "updated_at")+") VALUES ("+s.Placeholder(1)+", "+s.Placeholder(2)+", "+s.Placeholder(3)+", "+s.Placeholder(4)+", "+s.Placeholder(5)+", "+s.Placeholder(6)+")", "integration_credential_refresh_lease:"+workspaceID+":"+connectionKey, workspaceID, connectionKey, owner, expiresAt, now)
	if err == nil {
		return true, nil
	}
	var existing string
	readErr := r.db.QueryRowContext(ctx, "SELECT "+s.Identifier("lease_owner")+" FROM "+s.TableIdentifier("integration_credential_refresh_leases")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(2), workspaceID, connectionKey).Scan(&existing)
	if readErr == nil {
		return false, nil
	}
	return false, fmt.Errorf("insert credential refresh lease: %w", err)
}

func (r IntegrationConfigStore) ReleaseCredentialRefreshLease(ctx context.Context, workspaceID, connectionKey, owner string) error {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	s := r.store
	_, err = r.db.ExecContext(ctx, "DELETE FROM "+s.TableIdentifier("integration_credential_refresh_leases")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("connection_key")+" = "+s.Placeholder(2)+" AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(3), workspaceID, strings.TrimSpace(connectionKey), strings.TrimSpace(owner))
	if err != nil {
		return fmt.Errorf("release credential refresh lease: %w", err)
	}
	return nil
}
