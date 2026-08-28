package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func TestFileArtifactStoreReconcilesReferencesAndDeletesOnlyMatureOrphans(t *testing.T) {
	store := openLifecycleStore(t)
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: "text"}}}
	downloadTask := definitionmodel.ObjectSchema{Key: "download_task", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "token_status", Type: "text"}, {Key: "file_name", Type: "text"}, {Key: "report_export_audit", Type: "text"}}}
	exportAudit := definitionmodel.ObjectSchema{Key: "report_export_audit", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	objects := []definitionmodel.ObjectSchema{object, downloadTask, exportAudit}
	manifest := manifestmodel.ManifestSchema{TemplateID: "file-lifecycle", Version: "1", Name: "File lifecycle", Objects: objects}
	if err := metadatapersistence.NewMetadataStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "file lifecycle test"), manifest); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	artifactStore := NewFileArtifactStore(store, objects, root)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	records := recordpersistence.NewRecordStore(store)
	old := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
	if err := records.InsertRecord(t.Context(), "workspace-a", exportAudit, recordmodel.Record{ID: "audit-1", CreatedAt: old, UpdatedAt: old, Data: map[string]any{"status": "completed"}}); err != nil {
		t.Fatal(err)
	}
	if err := records.InsertRecord(t.Context(), "workspace-a", downloadTask, recordmodel.Record{ID: "download-1", CreatedAt: old, UpdatedAt: old, Data: map[string]any{"status": "ready", "token_status": "active", "file_name": "report.csv", "report_export_audit": "audit-1"}}); err != nil {
		t.Fatal(err)
	}
	filename := "content-addressed.txt"
	path := lifecycleTestUploadPath(t, root, "workspace-a", filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := artifactStore.RegisterUpload(t.Context(), lifecyclecontract.UploadArtifact{WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: filename, ContentType: "text/plain", SHA256: "hash", Size: 7, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	result, err := artifactStore.ReconcileUploadArtifacts(t.Context(), now.Add(time.Hour), 100)
	if err != nil || result.Deleted != 0 || result.Orphaned != 0 || result.ExpiredDownloads != 1 {
		t.Fatalf("staging grace result=%#v err=%v", result, err)
	}
	expiredTask, found, err := records.GetRecord(t.Context(), "workspace-a", downloadTask, "download-1")
	if err != nil || !found || expiredTask.Data["status"] != "expired" || expiredTask.Data["token_status"] != "expired" {
		t.Fatalf("expired task=%#v found=%v err=%v", expiredTask, found, err)
	}
	if filename, present := expiredTask.Data["file_name"]; present && filename != "" && filename != nil {
		t.Fatalf("expired download retained file name: %#v", filename)
	}
	if _, found, err := records.GetRecord(t.Context(), "workspace-a", exportAudit, "audit-1"); err != nil || !found {
		t.Fatalf("minimal export audit removed: found=%v err=%v", found, err)
	}
	created := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	if err := records.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "asset-1", CreatedAt: created, UpdatedAt: created, Data: map[string]any{"file_url": "/uploads/" + filename}}); err != nil {
		t.Fatal(err)
	}
	result, err = artifactStore.ReconcileUploadArtifacts(t.Context(), now.Add(2*time.Hour), 100)
	if err != nil || result.Referenced != 1 {
		t.Fatalf("referenced result=%#v err=%v", result, err)
	}
	record, _, err := records.GetRecord(t.Context(), "workspace-a", object, "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	record.Data["file_url"], record.UpdatedAt = nil, now.Add(3*time.Hour).Format(time.RFC3339Nano)
	if err := records.UpdateRecord(t.Context(), "workspace-a", object, record); err != nil {
		t.Fatal(err)
	}
	result, err = artifactStore.ReconcileUploadArtifacts(t.Context(), now.Add(3*time.Hour), 100)
	if err != nil || result.Orphaned != 1 || result.Deleted != 0 {
		t.Fatalf("orphan result=%#v err=%v", result, err)
	}
	result, err = artifactStore.ReconcileUploadArtifacts(t.Context(), now.Add(28*time.Hour), 100)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("delete result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mature orphan remains: %v", err)
	}
	var status, hash string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status, sha256 FROM lifecycle_file_artifacts WHERE workspace_id = ? AND filename = ?", "workspace-a", filename).Scan(&status, &hash); err != nil || status != "deleted" || hash != "hash" {
		t.Fatalf("status=%s hash=%s err=%v", status, hash, err)
	}
}

func TestFileArtifactStorePersistsPendingThenTrustedTerminalScan(t *testing.T) {
	store := openLifecycleStore(t)
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: "text"}}}
	artifactStore := NewFileArtifactStore(store, []definitionmodel.ObjectSchema{object}, t.TempDir())
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	artifact := lifecyclecontract.UploadArtifact{ID: "file-1", WorkspaceID: "workspace-a", ObjectKey: "asset", FieldKey: "file_url", Filename: "content.txt", ContentType: "text/plain", SHA256: "abc", Size: 7, CreatedAt: now}
	if err := artifactStore.RegisterUpload(t.Context(), artifact); err != nil {
		t.Fatal(err)
	}
	pending, err := artifactStore.FindFileScan(t.Context(), "workspace-a", "file-1")
	if err != nil || pending.Status != lifecyclecontract.FileScanPending || pending.SHA256 != "abc" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	clean := pending
	clean.Status, clean.Provider, clean.EvidenceRef, clean.ScannedAt = lifecyclecontract.FileScanClean, "scanner", "scan-1", now.Add(time.Minute)
	if err := artifactStore.RecordFileScan(t.Context(), clean); err != nil {
		t.Fatal(err)
	}
	stored, err := artifactStore.FindFileScan(t.Context(), "workspace-a", "file-1")
	if err != nil || stored.Status != lifecyclecontract.FileScanClean || stored.Provider != "scanner" || stored.EvidenceRef != "scan-1" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	clean.SHA256 = "tampered"
	if err := artifactStore.RecordFileScan(t.Context(), clean); err == nil {
		t.Fatal("expected content identity mismatch")
	}
}

func lifecycleTestUploadPath(t *testing.T, root, workspaceID, filename string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(workspaceID))
	return filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]), filename)
}
