package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestBusinessAuditExportStorePersistsFinalBytesHashAndIdempotentDownloadAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "business-audit-export.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store
	}
	content := []byte("\xef\xbb\xbfaudit_id,event\naudit-1,order.completed\n")
	contentSum := sha256.Sum256(content)
	token := "audexp_bearer_that_must_never_be_persisted"
	tokenSum := sha256.Sum256([]byte(token))
	artifact := auditmodel.AuditBusinessExportArtifact{
		WorkspaceID: "workspace-a", RequesterUserID: "auditor-a", RoleKey: "business-auditor", IdempotencyKey: "idem-1",
		Filters:     auditmodel.AuditBusinessExportFilter{Event: "order.completed", ActorID: "auditor-a"},
		ScopeSHA256: "scope-hash", AuthorizationScopeSHA256: "authorization-hash", TokenSHA256: hex.EncodeToString(tokenSum[:]),
		Filename: "business-audit-events.csv", ContentSHA256: hex.EncodeToString(contentSum[:]), RowCount: 1, Content: content,
		AuditIdentity: "business_audit_events:immutable", Status: "prepared", CreatedAt: "2026-08-12T00:00:00Z", ExpiresAt: "2026-08-12T00:15:00Z",
	}
	store := open()
	repository := NewAuditBusinessExportStore(store)
	created, wasCreated, err := repository.CreateOrGetBusinessAuditExport(t.Context(), artifact)
	if err != nil || !wasCreated {
		t.Fatalf("created=%#v wasCreated=%v err=%v", created, wasCreated, err)
	}
	if replay, createdAgain, err := repository.CreateOrGetBusinessAuditExport(t.Context(), artifact); err != nil || createdAgain || replay.ID != created.ID {
		t.Fatalf("replay=%#v created=%v err=%v", replay, createdAgain, err)
	}
	var persistedTokenHash string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT token_sha256 FROM "+store.TableIdentifier("business_audit_export_artifacts")+" WHERE id = ?", created.ID).Scan(&persistedTokenHash); err != nil {
		t.Fatal(err)
	}
	if persistedTokenHash == token || persistedTokenHash != artifact.TokenSHA256 {
		t.Fatalf("persisted token material=%q", persistedTokenHash)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restartedStore := open()
	t.Cleanup(func() { _ = restartedStore.Close() })
	restarted := NewAuditBusinessExportStore(restartedStore)
	loaded, found, err := restarted.BusinessAuditExportByTokenHash(t.Context(), artifact.WorkspaceID, artifact.TokenSHA256)
	if err != nil || !found || loaded.ID != created.ID || string(loaded.Content) != string(content) || loaded.ContentSHA256 != artifact.ContentSHA256 || loaded.AuditIdentity != artifact.AuditIdentity {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	first, err := restarted.RecordBusinessAuditExportDownload(t.Context(), loaded.WorkspaceID, loaded.ID, "2026-08-12T00:01:00Z")
	if err != nil || !first {
		t.Fatalf("first=%v err=%v", first, err)
	}
	first, err = restarted.RecordBusinessAuditExportDownload(t.Context(), loaded.WorkspaceID, loaded.ID, "2026-08-12T00:02:00Z")
	if err != nil || first {
		t.Fatalf("replayed first=%v err=%v", first, err)
	}
	loaded, found, err = restarted.BusinessAuditExportByTokenHash(t.Context(), artifact.WorkspaceID, artifact.TokenSHA256)
	if err != nil || !found || loaded.DownloadCount != 1 || loaded.LastDownloadedAt != "2026-08-12T00:01:00Z" || loaded.Status != "downloaded" {
		t.Fatalf("download receipt=%#v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := restarted.BusinessAuditExportByTokenHash(t.Context(), "workspace-b", artifact.TokenSHA256); err != nil || found {
		t.Fatalf("cross-workspace found=%v err=%v", found, err)
	}
}
