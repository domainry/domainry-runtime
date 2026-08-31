package integration

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
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
	statement, args, buildErr := query.NewWorkspaceUpdateBuilder(s.SQLRenderer, "_integration_credential_refresh_leases", workspaceID).
		Set("lease_owner", owner).Set("lease_expires_at", expiresAt).Set("updated_at", now).Where(query.And(
		query.Equal("connection_key", connectionKey),
		query.Or(query.Equal("lease_owner", owner), query.LessThanOrEqual("lease_expires_at", now)),
	)).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build credential refresh lease update: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
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
	statement, args, buildErr = query.NewWorkspaceInsertBuilder(s.SQLRenderer, "_integration_credential_refresh_leases", workspaceID).
		Columns("id", "connection_key", "lease_owner", "lease_expires_at", "updated_at").
		Values("integration_credential_refresh_lease:"+workspaceID+":"+connectionKey, connectionKey, owner, expiresAt, now).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build credential refresh lease insert: %w", buildErr)
	}
	_, err = r.db.ExecContext(ctx, statement, args...)
	if err == nil {
		return true, nil
	}
	var existing string
	queryValue, queryArgs, buildErr := query.NewWorkspaceSelectBuilder(s.SQLRenderer, "_integration_credential_refresh_leases", workspaceID).
		Columns("lease_owner").Where(query.Equal("connection_key", connectionKey)).Limit(1).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build credential refresh lease read: %w", buildErr)
	}
	readErr := r.db.QueryRowContext(ctx, queryValue, queryArgs...).Scan(&existing)
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
	statement, args, buildErr := query.NewWorkspaceDeleteBuilder(s.SQLRenderer, "_integration_credential_refresh_leases", workspaceID).
		Where(query.And(
			query.Equal("connection_key", strings.TrimSpace(connectionKey)),
			query.Equal("lease_owner", strings.TrimSpace(owner)),
		)).Build()
	if buildErr != nil {
		return fmt.Errorf("build credential refresh lease release: %w", buildErr)
	}
	_, err = r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return fmt.Errorf("release credential refresh lease: %w", err)
	}
	return nil
}
