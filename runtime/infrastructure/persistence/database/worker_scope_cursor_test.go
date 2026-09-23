package database

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkerScopeOwnersRequireRegisteredRecoveryPolicy(t *testing.T) {
	for _, owner := range []string{
		WorkerScopeOwnerAgentConversationCapacity,
		WorkerScopeOwnerAgentTask,
		WorkerScopeOwnerDataExchange,
		WorkerScopeOwnerIdempotencyCleanup,
		WorkerScopeOwnerNotificationChannel,
		WorkerScopeOwnerNotificationInbox,
		WorkerScopeOwnerRuntimePublicationOutbox,
		WorkerScopeOwnerSchedulerTriggerCapacity,
		WorkerScopeOwnerWorkflowContinuation,
	} {
		registration, found := WorkerScopeRegistrationFor(owner)
		if !found || registration.Owner != owner || strings.TrimSpace(registration.RecoveryPolicy) == "" {
			t.Fatalf("worker scope owner %q registration=%#v found=%v", owner, registration, found)
		}
	}
	if _, found := WorkerScopeRegistrationFor("unregistered"); found {
		t.Fatal("unregistered worker scope owner accepted")
	}
	store := openMigrationEdgeStore(t)
	if err := store.RegisterWorkerQueueScope(t.Context(), store.DB(), "unregistered", "workspace-a", "now"); err == nil {
		t.Fatal("unregistered worker scope owner wrote durable state")
	}
}

func TestWorkerQueueScopePageRotatesThroughBoundedWindows(t *testing.T) {
	store := openMigrationEdgeStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _worker_scopes (id TEXT PRIMARY KEY, owner TEXT NOT NULL, scope_key TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 70; index++ {
		key := fmt.Sprintf("workspace-%03d", index)
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _worker_scopes (id, owner, scope_key, updated_at) VALUES (?, ?, ?, ?)`, key, WorkerScopeOwnerNotificationInbox, key, "now"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(first) != 32 || first[0] != "workspace-000" || first[31] != "workspace-031" {
		t.Fatalf("first=%v err=%v", first, err)
	}
	second, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(second) != 32 || second[0] != "workspace-032" || second[31] != "workspace-063" {
		t.Fatalf("second=%v err=%v", second, err)
	}
	third, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(third) != 6 || third[0] != "workspace-064" || third[5] != "workspace-069" {
		t.Fatalf("third=%v err=%v", third, err)
	}
	wrapped, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(wrapped) != 32 || wrapped[0] != "workspace-000" {
		t.Fatalf("wrapped=%v err=%v", wrapped, err)
	}
}

func TestWorkerQueueScopePageWrapsWithoutEmptyPollAtExactWindow(t *testing.T) {
	store := openMigrationEdgeStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _worker_scopes (id TEXT PRIMARY KEY, owner TEXT NOT NULL, scope_key TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 32; index++ {
		key := fmt.Sprintf("workspace-%03d", index)
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _worker_scopes (id, owner, scope_key, updated_at) VALUES (?, ?, ?, ?)`, key, WorkerScopeOwnerNotificationInbox, key, "now"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(first) != 32 {
		t.Fatalf("first=%v err=%v", first, err)
	}
	wrapped, err := store.WorkerQueueScopePage(t.Context(), store.DB(), WorkerScopeOwnerNotificationInbox, 32)
	if err != nil || len(wrapped) != 32 || wrapped[0] != "workspace-000" {
		t.Fatalf("wrapped=%v err=%v", wrapped, err)
	}
}
