package auditmodule_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	"github.com/domainry/domainry-audit-sdk/contract"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type auditExportClock struct{ now time.Time }

func (c auditExportClock) Now() time.Time { return c.now }

func TestAuditExportUsesRuntimeSharedArtifactMetadataAndBlobContent(t *testing.T) {
	ctx := t.Context()
	store, err := database.OpenContext(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-export.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		t.Fatal(err)
	}

	blobs, err := blobstore.NewLocalStore(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	content := blobstore.LifecycleContentStore{Blobs: blobs}
	now := time.Date(2026, 9, 23, 14, 0, 0, 0, time.UTC)
	binding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{Clock: auditExportClock{now: now}}).OpenModule(
		ctx,
		auditsdk.ApplicationRef{InstallationID: "runtime-audit-export-test"},
		runtimeauditmodule.NewHost(store, content, content, operationspersistence.NewSharedCommandStore(store)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	if !binding.Descriptor().Capabilities.Export {
		t.Fatal("Runtime Audit binding did not advertise the shared Artifact-backed export capability")
	}

	if _, err := binding.Appender().Append(ctx, contract.AppendRequest{
		Family: contract.EventFamilyBusinessEntity,
		Event:  "order.completed", ObjectKey: "order", RecordID: "order-1",
		Actor:    contract.Actor{WorkspaceID: "workspace-a", SubjectID: "user-a", RoleKey: "member", RequestID: "source-request"},
		Metadata: map[string]any{"result": "completed", "reason": "order_fulfilled"},
	}); err != nil {
		t.Fatal(err)
	}

	binding.Exporter().ConfigureExport([]byte("0123456789abcdef0123456789abcdef"), nil)
	principal := contract.ExportPrincipal{
		WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "member",
		AuthorizationRevision: "revision-1", RequestID: "prepare-request",
	}
	prepared, err := binding.Exporter().PrepareExport(ctx, contract.ExportRequest{}, "export-idempotency-1", principal)
	if err != nil {
		t.Fatal(err)
	}

	var owner, kind, storageReference, contentSHA256 string
	var sizeBytes int64
	if err := store.DB().QueryRowContext(ctx, `
		SELECT owner, kind, storage_reference, content_sha256, size_bytes
		FROM _artifacts
		WHERE workspace_id = ? AND id = ?`, principal.WorkspaceID, prepared.ID,
	).Scan(&owner, &kind, &storageReference, &contentSHA256, &sizeBytes); err != nil {
		t.Fatal(err)
	}
	if owner != "audit" || kind != "export" || storageReference == "" || contentSHA256 != prepared.ContentSHA256 || sizeBytes <= 0 {
		t.Fatalf("shared artifact metadata owner=%q kind=%q ref=%q sha=%q size=%d prepared=%+v", owner, kind, storageReference, contentSHA256, sizeBytes, prepared)
	}
	var requesterBindings int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM _artifact_bindings
		WHERE workspace_id = ? AND artifact_id = ? AND owner = 'audit'
		  AND kind = 'subject' AND resource_type = 'audit_export_requester'
		  AND resource_id = ?`, principal.WorkspaceID, prepared.ID, principal.UserID,
	).Scan(&requesterBindings); err != nil || requesterBindings != 1 {
		t.Fatalf("requester bindings=%d err=%v", requesterBindings, err)
	}
	var operationID, operationStatus, operationResult string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT id, status, result_json
		FROM _operations
		WHERE workspace_id = ? AND owner = 'audit' AND kind = 'audit.export.prepare'
		  AND idempotency_key = ?`, principal.WorkspaceID, "export-idempotency-1",
	).Scan(&operationID, &operationStatus, &operationResult); err != nil {
		t.Fatal(err)
	}
	if operationStatus != "succeeded" || !bytes.Contains([]byte(operationResult), []byte(prepared.ID)) {
		t.Fatalf("prepare operation id=%q status=%q result=%s", operationID, operationStatus, operationResult)
	}
	var operationBindings int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM _artifact_bindings
		WHERE workspace_id = ? AND artifact_id = ? AND owner = 'audit'
		  AND kind = 'operation' AND resource_type = 'operation'
		  AND resource_id = ?`, principal.WorkspaceID, prepared.ID, operationID,
	).Scan(&operationBindings); err != nil || operationBindings != 1 {
		t.Fatalf("operation bindings=%d err=%v", operationBindings, err)
	}
	var privateExportTables int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = '_audit_export_artifacts'`,
	).Scan(&privateExportTables); err != nil || privateExportTables != 0 {
		t.Fatalf("private Audit export tables=%d err=%v", privateExportTables, err)
	}
	blobInfo, err := content.Stat(ctx, principal.WorkspaceID, storageReference)
	if err != nil || blobInfo.SHA256 != contentSHA256 || blobInfo.Size != sizeBytes {
		t.Fatalf("blob info=%+v err=%v", blobInfo, err)
	}

	downloadPrincipal := principal
	downloadPrincipal.RequestID = "download-request-1"
	exported, filename, err := binding.Exporter().DownloadExport(ctx, prepared.DownloadToken, downloadPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if filename != prepared.Filename || int64(len(exported)) != sizeBytes || !bytes.Contains(exported, []byte("order.completed")) {
		t.Fatalf("download filename=%q size=%d expected_size=%d content=%q", filename, len(exported), sizeBytes, exported)
	}
	downloadPrincipal.RequestID = "download-request-2"
	if replay, replayFilename, replayErr := binding.Exporter().DownloadExport(ctx, prepared.DownloadToken, downloadPrincipal); replayErr != nil || replayFilename != filename || !bytes.Equal(replay, exported) {
		t.Fatalf("repeat download filename=%q size=%d err=%v", replayFilename, len(replay), replayErr)
	}
	events, err := binding.Reader().List(ctx, principal.WorkspaceID, contract.Query{Event: "audit_export_downloaded", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metadata["request_id"] != "download-request-1" {
		t.Fatalf("download audit events=%+v", events)
	}
}
