package upload_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/entityid"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	upload "github.com/domainry/domainry-runtime/runtime/application/upload"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

type uploadedFileFields struct{}

func (uploadedFileFields) HasUploadField(objectKey, fieldKey string) bool {
	return objectKey == "interaction" && fieldKey == "attachments"
}

func TestIncomingUploadPersistsCanonicalArtifactAndSubjectInSQLite(t *testing.T) {
	const (
		workspaceID = "workspace-upload-integration"
		userID      = "user-upload-integration"
	)
	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver:       "sqlite",
		DBPath:               filepath.Join(t.TempDir(), "runtime.db"),
		IntegrationSecretKey: "upload-integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	binding, err := lifecyclesdkfixture.Open(t.Context(), store, "upload-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(t.Context()) })
	artifacts, err := binding.UploadArtifacts(lifecyclesdk.UploadArtifactOptions{Root: t.TempDir(), Fields: uploadedFileFields{}})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := upload.NewFileCapabilityService(artifacts, upload.NewFileScanReceiptVerifier(artifacts, bytes.Repeat([]byte("k"), 32)), blobs, func() time.Time {
		return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	service.BindUploadSubjects(upload.NewUploadSubjectRegistry(store))
	policy, err := recordmodel.RecordFileFieldPolicyFor(definitionmodel.FieldSchema{
		Key: "attachments", Type: recordmodel.RecordFileListFieldType,
		Config: map[string]any{"allowed_mime_types": []any{"text/plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: userID}}
	content := []byte("customer diagnostic log")
	digest := sha256.Sum256(content)
	request := runtimeext.UploadedFileRequest{
		IdempotencyKey: "message-1:attachment-1", ObjectKey: "interaction", FieldKey: "attachments",
		Filename: "diagnostic.log", ContentType: "text/plain", ContentSHA256: hex.EncodeToString(digest[:]),
		Size: int64(len(content)), Content: bytes.NewReader(content),
	}
	first, err := service.IngestUploaded(t.Context(), workspaceID, principal, request, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := entityid.Validate("art", first.FileID); err != nil {
		t.Fatalf("file ID %q is not a canonical Artifact ID: %v", first.FileID, err)
	}
	for table, want := range map[string]int{"_artifacts": 1, "_artifact_bindings": 1} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE workspace_id = ? AND artifact_id = ?", workspaceID, first.FileID).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	request.Content = strings.NewReader(string(content))
	replay, err := service.IngestUploaded(t.Context(), workspaceID, principal, request, policy)
	if err != nil {
		t.Fatal(err)
	}
	if replay.FileID != first.FileID || replay.Status != first.Status {
		t.Fatalf("idempotent replay changed upload: first=%+v replay=%+v", first, replay)
	}
	for _, table := range []string{"_artifacts", "_artifact_bindings"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE workspace_id = ? AND artifact_id = ?", workspaceID, first.FileID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("idempotent replay duplicated %s: count=%d err=%v", table, count, err)
		}
	}
}
