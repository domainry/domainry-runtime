package record_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

func TestRegisteredAbandonedUploadIsExportedAndDeletedFromFrozenPlan(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "upload-erasure.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspaceHash := sha256.Sum256([]byte("workspace-a"))
	directory := filepath.Join(root, "workspace-"+hex.EncodeToString(workspaceHash[:16]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "abandoned.png")
	if err := os.WriteFile(path, []byte("unattached avatar"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUploadSubject(t.Context(), uploadapplication.UploadSubjectBinding{WorkspaceID: "workspace-a", FileID: "file-a", Filename: "abandoned.png", ObjectKey: "member_avatar_upload", FieldKey: "file_url", UserID: "alice", SHA256: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	binding, err := lifecyclesdkfixture.Open(t.Context(), store, "upload-erasure")
	if err != nil {
		t.Fatal(err)
	}
	localContent, err := blobstore.NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := binding.SubjectArtifacts(root, blobstore.LifecycleContentStore{Blobs: localContent})
	if err != nil {
		t.Fatal(err)
	}
	service := recordapplication.NewRecordSubjectLifecycleApplicationService(recordpersistence.NewRecordStore(store), nil, files)
	service.BindSubjectUploads(store.SubjectUploadReferences)
	preview, err := service.PreviewSubject(t.Context(), "workspace-a", "alice")
	if err != nil || !strings.Contains(string(preview), `"files":1`) {
		t.Fatalf("preview=%s %v", preview, err)
	}
	export, err := service.ExportSubject(t.Context(), "workspace-a", "alice")
	if err != nil || !strings.Contains(string(export), "abandoned.png") {
		t.Fatalf("export=%s %v", export, err)
	}
	plan, err := service.PrepareSubjectErasure(t.Context(), "erase-upload", "workspace-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(plan) || strings.Contains(string(plan), "unattached avatar") {
		t.Fatalf("invalid frozen plan=%s", plan)
	}
	for i := 0; i < 2; i++ {
		if _, err := service.ErasePreparedSubject(t.Context(), "erase-upload", "workspace-a", "alice", plan, nil); err != nil {
			t.Fatalf("erase attempt %d: %v", i, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("abandoned upload survived: %v", err)
	}
}
