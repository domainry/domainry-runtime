package integration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCredentialRefreshLeaseCoordinatesRepositoriesAndRecoversExpiry(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := NewIntegrationConfigStore(store)
	second := NewIntegrationConfigStore(store)
	now := time.Now().UTC()
	acquired, err := first.TryAcquireCredentialRefreshLease(t.Context(), "workspace", "connection", "owner-a", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil || !acquired {
		t.Fatalf("first acquire=%v error=%v", acquired, err)
	}
	acquired, err = second.TryAcquireCredentialRefreshLease(t.Context(), "workspace", "connection", "owner-b", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil || acquired {
		t.Fatalf("competing acquire=%v error=%v", acquired, err)
	}
	if err := first.ReleaseCredentialRefreshLease(t.Context(), "workspace", "connection", "owner-a"); err != nil {
		t.Fatal(err)
	}
	acquired, err = second.TryAcquireCredentialRefreshLease(t.Context(), "workspace", "connection", "owner-b", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil || !acquired {
		t.Fatalf("acquire after release=%v error=%v", acquired, err)
	}

	past := now.Add(-2 * time.Minute)
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE integration_credential_refresh_leases SET lease_expires_at = ? WHERE workspace_id = ? AND connection_key = ?", past.Format(time.RFC3339), "workspace", "connection"); err != nil {
		t.Fatal(err)
	}
	acquired, err = first.TryAcquireCredentialRefreshLease(t.Context(), "workspace", "connection", "owner-c", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil || !acquired {
		t.Fatalf("expired takeover=%v error=%v", acquired, err)
	}
}

func TestCredentialRefreshLeaseHonorsCancelledContext(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	now := time.Now().UTC()
	_, err := NewIntegrationConfigStore(store).TryAcquireCredentialRefreshLease(ctx, "workspace", "connection", "owner", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error=%v", err)
	}
}
