package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestAgentStateStoreContractAndCancellation(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "agent-state.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := NewAgentStateStore(store)
	first := agentmodel.AgentStateRecord{Kind: "session", Key: "default:user:role:s1", WorkspaceID: "default", UserID: "user", RoleKey: "role", Payload: json.RawMessage(`{"title":"First"}`), UpdatedAt: 1}
	if err := repository.Put(t.Context(), "default", first); err != nil {
		t.Fatalf("put state: %v", err)
	}
	first.Payload = json.RawMessage(`{"title":"Updated"}`)
	first.UpdatedAt = 2
	if err := repository.Put(t.Context(), "default", first); err != nil {
		t.Fatalf("replace state: %v", err)
	}
	loaded, found, err := repository.Get(t.Context(), "default", first.Kind, first.Key)
	if err != nil || !found || loaded.UpdatedAt != 2 || string(loaded.Payload) != string(first.Payload) {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	cas := first
	cas.Payload, cas.UpdatedAt = json.RawMessage(`{"title":"CAS"}`), 4
	if updated, err := repository.CompareAndSwap(t.Context(), "default", cas, 1); err != nil || updated {
		t.Fatalf("stale CAS updated=%v err=%v", updated, err)
	}
	if updated, err := repository.CompareAndSwap(t.Context(), "default", cas, 2); err != nil || !updated {
		t.Fatalf("live CAS updated=%v err=%v", updated, err)
	}
	values, err := repository.List(t.Context(), "default", "session", "user", "role")
	if err != nil || len(values) != 1 || values[0].Key != first.Key {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	batch := []agentmodel.AgentStateRecord{
		{Kind: "report_export_audit", Key: "q1", WorkspaceID: "default", UserID: "user", RoleKey: "role", Payload: json.RawMessage(`{"status":"prepared"}`), UpdatedAt: 3},
		{Kind: "report_download_task", Key: "q1", WorkspaceID: "default", UserID: "user", RoleKey: "role", Payload: json.RawMessage(`{"status":"prepared"}`), UpdatedAt: 3},
	}
	if err := repository.PutBatch(t.Context(), "default", batch); err != nil {
		t.Fatalf("put batch: %v", err)
	}
	for _, expected := range batch {
		loaded, found, err := repository.Get(t.Context(), "default", expected.Kind, expected.Key)
		if err != nil || !found || string(loaded.Payload) != string(expected.Payload) {
			t.Fatalf("batch loaded=%#v found=%v err=%v", loaded, found, err)
		}
	}
	duplicateFirst := batch[0]
	duplicateFirst.Payload = json.RawMessage(`{"status":"first"}`)
	duplicateLast := batch[0]
	duplicateLast.Payload = json.RawMessage(`{"status":"last"}`)
	duplicateLast.UpdatedAt = 5
	if err := repository.PutBatch(t.Context(), "default", []agentmodel.AgentStateRecord{duplicateFirst, duplicateLast}); err != nil {
		t.Fatalf("put duplicate batch: %v", err)
	}
	loaded, found, err = repository.Get(t.Context(), "default", duplicateLast.Kind, duplicateLast.Key)
	if err != nil || !found || loaded.UpdatedAt != 5 || string(loaded.Payload) != string(duplicateLast.Payload) {
		t.Fatalf("duplicate batch last-write-wins loaded=%#v found=%v err=%v", loaded, found, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.List(cancelled, "default", "session", "user", "role"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestAgentStateStoreWorkspaceIsolationContract(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "agent-workspace.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := NewAgentStateStore(store)

	const kind, key = "session", "shared-key"
	stateA := agentmodel.AgentStateRecord{Kind: kind, Key: key, WorkspaceID: "workspace-a", UserID: "user", RoleKey: "role", Payload: json.RawMessage(`{"workspace":"a"}`), UpdatedAt: 1}
	stateB := agentmodel.AgentStateRecord{Kind: kind, Key: key, WorkspaceID: "workspace-b", UserID: "user", RoleKey: "role", Payload: json.RawMessage(`{"workspace":"b"}`), UpdatedAt: 2}
	if err := repository.Put(t.Context(), "workspace-a", stateA); err != nil {
		t.Fatalf("put workspace A: %v", err)
	}
	if err := repository.Put(t.Context(), "workspace-b", stateB); err != nil {
		t.Fatalf("put workspace B: %v", err)
	}
	for _, test := range []struct {
		workspaceID string
		payload     string
	}{{"workspace-a", string(stateA.Payload)}, {"workspace-b", string(stateB.Payload)}} {
		loaded, found, err := repository.Get(t.Context(), test.workspaceID, kind, key)
		if err != nil || !found || string(loaded.Payload) != test.payload {
			t.Fatalf("workspace=%s loaded=%#v found=%v err=%v", test.workspaceID, loaded, found, err)
		}
		values, err := repository.List(t.Context(), test.workspaceID, kind, "user", "role")
		if err != nil || len(values) != 1 || string(values[0].Payload) != test.payload {
			t.Fatalf("workspace=%s values=%#v err=%v", test.workspaceID, values, err)
		}
	}
	if _, _, err := repository.Get(t.Context(), "", kind, key); err == nil {
		t.Fatal("expected empty workspace to be rejected")
	}
	if err := repository.Put(t.Context(), "workspace-a", stateB); err == nil {
		t.Fatal("expected mismatched record workspace to be rejected")
	}
}
