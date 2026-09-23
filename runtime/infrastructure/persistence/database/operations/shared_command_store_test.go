package operations

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSharedCommandStoreClaimsCompletesAndReplaysCanonicalOperation(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "shared-operations.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewSharedCommandStore(runtimeStore)
	now := time.Now().UTC()
	command := sharedoperation.Command{
		ID: "scheduler-operation-1", Scope: sharedoperation.Scope{SystemPurpose: "scheduler_management", ResourceType: "scheduler_command", ResourceID: "runtime-a:/scheduler/definitions/daily/run"},
		Owner: "scheduler", Kind: "management_command", ActionKey: "scheduler.definitions.run",
		IdempotencyKey: "runtime-a:run-01", RequestFingerprint: "hash-a", RequestedBy: "actor-a", Reason: "run schedule",
		StatusURL: "/scheduler/definitions/daily/run", CreatedAt: now,
	}
	claimed, first, err := store.Claim(t.Context(), command)
	if err != nil || !first || claimed.Status != sharedoperation.StatusStarted {
		t.Fatalf("claimed=%+v first=%t err=%v", claimed, first, err)
	}
	replayed, first, err := store.Claim(t.Context(), command)
	if err != nil || first || replayed.Status != sharedoperation.StatusStarted {
		t.Fatalf("replayed=%+v first=%t err=%v", replayed, first, err)
	}
	conflict := command
	conflict.RequestFingerprint = "hash-b"
	if _, _, err := store.Claim(t.Context(), conflict); !errors.Is(err, sharedoperation.ErrIdempotencyConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	result := json.RawMessage(`{"http_status":200,"response_json":"eyJvayI6dHJ1ZX0="}`)
	if err := store.Complete(t.Context(), sharedoperation.Completion{
		ID: command.ID, Scope: command.Scope, Owner: command.Owner, Kind: command.Kind,
		IdempotencyKey: command.IdempotencyKey, RequestFingerprint: command.RequestFingerprint,
		Result: result, CompletedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	replayed, first, err = store.Claim(t.Context(), command)
	if err != nil || first || replayed.Status != sharedoperation.StatusSucceeded || string(replayed.Result) != string(result) {
		t.Fatalf("completed replay=%+v first=%t err=%v", replayed, first, err)
	}
	var rows int
	if err := runtimeStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE owner = 'scheduler' AND kind = 'management_command' AND system_purpose = 'scheduler_management'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("shared operation rows=%d err=%v", rows, err)
	}
}

func TestSharedCommandStoreRedactsInlineEvidenceAndRejectsLargeBodies(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "shared-result-policy.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewSharedCommandStore(runtimeStore)
	now := time.Now().UTC()
	command := sharedoperation.Command{
		ID: "operation-redacted", Scope: sharedoperation.Scope{WorkspaceID: "workspace-a", ResourceType: "command", ResourceID: "resource-a"},
		Owner: "owner", Kind: "command", ActionKey: "owner.command", IdempotencyKey: "redacted", RequestFingerprint: "hash",
		RequestedBy: "operator", Reason: "test result evidence", StatusURL: "/operations/operation-redacted", CreatedAt: now,
	}
	if _, claimed, err := store.Claim(t.Context(), command); err != nil || !claimed {
		t.Fatalf("claim claimed=%t err=%v", claimed, err)
	}
	if err := store.Complete(t.Context(), sharedoperation.Completion{
		ID: command.ID, Scope: command.Scope, Owner: command.Owner, Kind: command.Kind, IdempotencyKey: command.IdempotencyKey,
		RequestFingerprint: command.RequestFingerprint, Result: json.RawMessage(`{"ok":true,"access_token":"secret"}`), CompletedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	replayed, _, err := store.Claim(t.Context(), command)
	if err != nil || strings.Contains(string(replayed.Result), "secret") || !strings.Contains(string(replayed.Result), "[REDACTED]") {
		t.Fatalf("redacted replay=%s err=%v", replayed.Result, err)
	}

	large := command
	large.ID, large.IdempotencyKey, large.RequestFingerprint = "operation-large", "large", "large-hash"
	if _, claimed, err := store.Claim(t.Context(), large); err != nil || !claimed {
		t.Fatalf("large claim claimed=%t err=%v", claimed, err)
	}
	largeResult := json.RawMessage(`{"details":"` + strings.Repeat("x", 20*1024) + `"}`)
	if err := store.Complete(t.Context(), sharedoperation.Completion{
		ID: large.ID, Scope: large.Scope, Owner: large.Owner, Kind: large.Kind, IdempotencyKey: large.IdempotencyKey,
		RequestFingerprint: large.RequestFingerprint, Result: largeResult, CompletedAt: now.Add(2 * time.Second),
	}); err == nil || !strings.Contains(err.Error(), "shared Artifact") {
		t.Fatalf("large inline result error=%v", err)
	}
}
