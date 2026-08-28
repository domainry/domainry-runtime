package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
)

func TestLifecycleArtifactCleanupDeletesOnlyExpiredUploadStaging(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace-0123456789abcdef")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	oldTemporary := filepath.Join(workspace, ".upload-old")
	recentTemporary := filepath.Join(workspace, ".upload-recent")
	finalFile := filepath.Join(workspace, "content-addressed.pdf")
	for _, path := range []string{oldTemporary, recentTemporary, finalFile} {
		if err := os.WriteFile(path, []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(oldTemporary, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(recentTemporary, now.Add(-30*time.Minute), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(finalFile, now.Add(-24*time.Hour), now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	deleted, err := NewSubjectStore(root).DeleteExpiredUploadStaging(t.Context(), now)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := os.Stat(oldTemporary); !os.IsNotExist(err) {
		t.Fatalf("old staging file still exists: %v", err)
	}
	for _, path := range []string{recentTemporary, finalFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected artifact %s: %v", path, err)
		}
	}
}

func TestLifecycleSubjectFileExportAndDeleteAreWorkspaceScoped(t *testing.T) {
	root := t.TempDir()
	workspaceID := "workspace-a"
	digest := sha256.Sum256([]byte(workspaceID))
	workspace := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "evidence.txt")
	if err := os.WriteFile(path, []byte("subject evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewSubjectStore(root)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, ObjectKey: "profile", RecordID: "record-1", FieldKey: "attachment", Reference: "/uploads/evidence.txt"}
	evidence, err := store.ExportSubjectFile(t.Context(), reference)
	if err != nil || string(evidence.Content) != "subject evidence" || evidence.SHA256 == "" || evidence.Size != 16 {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
	deleted, err := store.DeleteSubjectFile(t.Context(), reference)
	if err != nil || len(deleted.Content) != 0 || deleted.SHA256 == "" {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("subject file remains: %v", err)
	}
	reference.WorkspaceID = "workspace-b"
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("cross-workspace subject file read accepted")
	}
}
