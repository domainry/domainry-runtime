package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
)

func TestLifecycleSubjectExportPutReadExpiryAndReferenceValidation(t *testing.T) {
	root := t.TempDir()
	store := NewSubjectStore(root)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PutSubjectExport(cancelled, "workspace-a", "request-1", json.RawMessage(`{"ok":true}`), time.Now().Add(time.Hour)); !errors.Is(err, context.Canceled) {
		t.Fatalf("put cancellation=%v", err)
	}
	for _, test := range []struct {
		workspace, request string
		expires            time.Time
	}{
		{workspace: "", request: "request-1", expires: time.Now().Add(time.Hour)},
		{workspace: "workspace-a", request: "", expires: time.Now().Add(time.Hour)},
		{workspace: "workspace-a", request: "request-1", expires: time.Now().Add(-time.Hour)},
	} {
		if _, err := store.PutSubjectExport(t.Context(), test.workspace, test.request, nil, test.expires); err == nil {
			t.Fatalf("invalid put=%+v accepted", test)
		}
	}
	payload := json.RawMessage(`{"subject":"data"}`)
	expires := time.Now().UTC().Add(time.Hour)
	reference, err := store.PutSubjectExport(t.Context(), "workspace-a", "request-1", payload, expires)
	if err != nil || !strings.HasPrefix(reference, "request-1-") {
		t.Fatalf("reference=%q err=%v", reference, err)
	}
	read, err := store.ReadSubjectExport(t.Context(), "workspace-a", reference, expires.Add(-time.Minute))
	if err != nil || string(read) != string(payload) {
		t.Fatalf("read=%s err=%v", read, err)
	}
	if _, err := store.ReadSubjectExport(t.Context(), "workspace-b", reference, expires.Add(-time.Minute)); err == nil {
		t.Fatal("cross-workspace export read accepted")
	}
	if _, err := store.ReadSubjectExport(t.Context(), "workspace-a", reference, expires); err == nil {
		t.Fatal("expired export read accepted")
	}
	if _, err := store.ReadSubjectExport(cancelled, "workspace-a", reference, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation=%v", err)
	}
	for _, invalid := range []string{"", "../escape", "a/b", "..value"} {
		if _, err := store.path(invalid); err == nil {
			t.Fatalf("invalid reference %q accepted", invalid)
		}
	}
	if path, err := store.path("valid-reference"); err != nil || filepath.Base(path) != "valid-reference.json" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if _, err := store.ReadSubjectExport(t.Context(), "workspace-a", "missing", time.Now()); !os.IsNotExist(err) {
		t.Fatalf("missing read error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(store.directory, "corrupt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadSubjectExport(t.Context(), "workspace-a", "corrupt", time.Now()); err == nil {
		t.Fatal("corrupt export accepted")
	}
}

func TestDeleteExpiredSubjectExportsSkipsProtectedAndCorruptEntries(t *testing.T) {
	root := t.TempDir()
	store := NewSubjectStore(root)
	now := time.Now().UTC()
	if deleted, err := store.DeleteExpiredSubjectExports(t.Context(), now); err != nil || deleted != 0 {
		t.Fatalf("missing directory deleted=%d err=%v", deleted, err)
	}
	if err := os.MkdirAll(filepath.Join(store.directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"expired.json": mustLifecycleArtifactJSON(t, lifecycleSubjectArtifact{WorkspaceID: "workspace-a", ExpiresAt: now.Add(-time.Minute), Payload: json.RawMessage(`{}`)}),
		"future.json":  mustLifecycleArtifactJSON(t, lifecycleSubjectArtifact{WorkspaceID: "workspace-a", ExpiresAt: now.Add(time.Minute), Payload: json.RawMessage(`{}`)}),
		"corrupt.json": []byte("{"),
		"ignored.txt":  []byte("ignored"),
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(store.directory, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.DeleteExpiredSubjectExports(t.Context(), now)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(store.directory, "expired.json")); !os.IsNotExist(err) {
		t.Fatal("expired export remains")
	}
	for _, name := range []string{"future.json", "corrupt.json", "ignored.txt"} {
		if _, err := os.Stat(filepath.Join(store.directory, name)); err != nil {
			t.Fatalf("protected file %s: %v", name, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.DeleteExpiredSubjectExports(cancelled, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup cancellation=%v", err)
	}
}

func mustLifecycleArtifactJSON(t *testing.T, artifact lifecycleSubjectArtifact) []byte {
	t.Helper()
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUploadStagingAndSubjectFileFailureEdges(t *testing.T) {
	if deleted, err := NewSubjectStore(t.TempDir()).DeleteExpiredUploadStaging(t.Context(), time.Now()); err != nil || deleted != 0 {
		t.Fatalf("empty staging deleted=%d err=%v", deleted, err)
	}
	if _, err := NewSubjectStore("").DeleteExpiredUploadStaging(t.Context(), time.Now()); err == nil {
		t.Fatal("empty upload root accepted")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "not-workspace"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace-test")
	if err := os.MkdirAll(filepath.Join(workspace, ".upload-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "final.txt"), []byte("final"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewSubjectStore(root).DeleteExpiredUploadStaging(cancelled, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("staging cancellation=%v", err)
	}

	store := NewSubjectStore(root)
	for _, reference := range []lifecyclecontract.SubjectFileReference{
		{WorkspaceID: "", Reference: "/uploads/file.txt"},
		{WorkspaceID: "workspace-a", Reference: ""},
		{WorkspaceID: "workspace-a", Reference: "/"},
	} {
		if _, _, err := store.subjectFilePath(reference); err == nil {
			t.Fatalf("invalid subject reference=%+v accepted", reference)
		}
	}
	if _, err := store.ExportSubjectFile(cancelled, lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/file.txt"}); !errors.Is(err, context.Canceled) && !os.IsNotExist(err) {
		t.Fatalf("export cancellation/path error=%v", err)
	}
	workspaceID := "workspace-a"
	digest := sha256.Sum256([]byte(workspaceID))
	directory := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(directory, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(lifecycleSubjectFileLimit + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, Reference: "/uploads/large.bin"}
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("oversized subject file exported")
	}
	missing := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, Reference: "/uploads/missing.txt"}
	if _, err := store.DeleteSubjectFile(t.Context(), missing); err == nil {
		t.Fatal("missing subject file deleted")
	}
}
