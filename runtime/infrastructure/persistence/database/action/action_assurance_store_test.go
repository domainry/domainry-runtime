package action

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestActionAssuranceStoreConsumesGrantExactlyOnce(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "assurance.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionAssuranceStore(store)
	grant := actionmodel.ActionAssuranceGrant{ID: "grant-1", TokenHash: "hash", WorkspaceID: "workspace-a", UserID: "user-a", ActionKey: "payment.refund", ObjectKey: "payment", RecordID: "payment-1", PayloadDigest: "digest", Methods: []string{"otp", "recent_reauth"}, ApprovalVersion: "7", ApprovalHash: "approval-hash", IssuedAt: "2026-07-21T10:00:00Z", ExpiresAt: "2026-07-21T10:05:00Z"}
	if err := repository.SaveActionAssuranceGrant(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.GetActionAssuranceGrant(t.Context(), grant.ID)
	if err != nil || !found || len(loaded.Methods) != 2 || loaded.ApprovalHash != grant.ApprovalHash {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	now := time.Date(2026, 7, 21, 10, 1, 0, 0, time.UTC)
	if consumed, err := repository.ConsumeActionAssuranceGrant(t.Context(), grant.WorkspaceID, grant.ID, now); err != nil || !consumed {
		t.Fatalf("first consume=%v err=%v", consumed, err)
	}
	if consumed, err := repository.ConsumeActionAssuranceGrant(t.Context(), grant.WorkspaceID, grant.ID, now); err != nil || consumed {
		t.Fatalf("second consume=%v err=%v", consumed, err)
	}
	loaded, found, err = repository.GetActionAssuranceGrant(t.Context(), grant.ID)
	if err != nil || !found || loaded.ConsumedAt == "" {
		t.Fatalf("consumed loaded=%#v err=%v", loaded, err)
	}
}

func TestActionAssuranceStoreFailureEdges(t *testing.T) {
	base, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "assurance-edges.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	wantErr := errors.New("assurance store failure")
	grant := actionmodel.ActionAssuranceGrant{ID: "grant", WorkspaceID: "workspace", Methods: []string{"otp"}}

	scripted := func(state *actionDBState) (ActionAssuranceStore, func()) {
		db := sql.OpenDB(actionConnector{state: state})
		return ActionAssuranceStore{store: base, db: db}, func() { _ = db.Close() }
	}

	repository, closeDB := scripted(&actionDBState{execSteps: []actionExecStep{{err: wantErr}}})
	if err := repository.SaveActionAssuranceGrant(t.Context(), grant); !errors.Is(err, wantErr) {
		t.Fatalf("save error=%v", err)
	}
	closeDB()

	columns := []string{"id", "token_hash", "workspace_id", "user_id", "action_key", "object_key", "record_id", "payload_digest", "methods_json", "approval_version", "approval_hash", "issued_at", "expires_at", "consumed_at"}
	repository, closeDB = scripted(&actionDBState{querySteps: []actionQueryStep{{columns: columns}}})
	if loaded, found, err := repository.GetActionAssuranceGrant(t.Context(), "missing"); err != nil || found || loaded.ID != "" {
		t.Fatalf("missing grant=%#v found=%v err=%v", loaded, found, err)
	}
	closeDB()

	repository, closeDB = scripted(&actionDBState{querySteps: []actionQueryStep{{err: wantErr}}})
	if _, _, err := repository.GetActionAssuranceGrant(t.Context(), "grant"); !errors.Is(err, wantErr) {
		t.Fatalf("get error=%v", err)
	}
	closeDB()

	row := []driver.Value{"grant", "hash", "workspace", "user", "action", "object", "record", "digest", "{", "", "", "", "", ""}
	repository, closeDB = scripted(&actionDBState{querySteps: []actionQueryStep{{columns: columns, rows: [][]driver.Value{row}}}})
	if _, _, err := repository.GetActionAssuranceGrant(t.Context(), "grant"); err == nil {
		t.Fatal("invalid methods JSON accepted")
	}
	closeDB()

	for _, step := range []actionExecStep{{err: wantErr}, {rowsErr: wantErr}} {
		repository, closeDB = scripted(&actionDBState{execSteps: []actionExecStep{step}})
		if _, err := repository.ConsumeActionAssuranceGrant(t.Context(), "workspace", "grant", time.Now()); !errors.Is(err, wantErr) {
			t.Fatalf("consume error=%v for step=%#v", err, step)
		}
		closeDB()
	}
}
