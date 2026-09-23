package blobstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

func TestLocalStoreStageCommitOpenStatDelete(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("immutable-content")
	staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "workspace-a", StageID: "upload-1", Content: bytes.NewReader(content), MaxBytes: int64(len(content)),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := staged.ContentSHA256 + "-upload-1.bin"
	committed, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{
		WorkspaceID: "workspace-a", StageKey: staged.BlobKey, BlobKey: key, ContentSHA256: staged.ContentSHA256, Size: staged.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed.BlobKey != key || committed.Size != int64(len(content)) || committed.ContentSHA256 != staged.ContentSHA256 {
		t.Fatalf("committed=%+v staged=%+v", committed, staged)
	}
	reader, err := store.Open(t.Context(), "workspace-a", key)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || !bytes.Equal(got, content) {
		t.Fatalf("content=%q err=%v", got, readErr)
	}
	if _, err := store.Open(t.Context(), "workspace-b", key); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("cross-workspace open err=%v", err)
	}
	if err := store.Delete(t.Context(), "workspace-a", key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), "workspace-a", key); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("deleted stat err=%v", err)
	}
}

func TestLifecycleContentStoreWritesImmutableArchiveContentIdempotently(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := LifecycleContentStore{Blobs: store}
	first, err := content.PutImmutable(t.Context(), "workspace-a", "archive-1", []byte(`{"id":"record-1"}`))
	if err != nil || first.Reference == "" || first.Size == 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replayed, err := content.PutImmutable(t.Context(), "workspace-a", "archive-1", []byte(`{"id":"record-1"}`))
	if err != nil || replayed != first {
		t.Fatalf("replayed=%+v first=%+v err=%v", replayed, first, err)
	}
	reader, err := content.Open(t.Context(), "workspace-a", first.Reference)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || string(raw) != `{"id":"record-1"}` {
		t.Fatalf("content=%s err=%v", raw, readErr)
	}
}

func TestLocalStoreRejectsOversizeAndIdentityConflictWithoutOverwrite(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: "workspace-a", StageID: "too-large", Content: strings.NewReader("12345"), MaxBytes: 4,
	}); !errors.Is(err, runtimefile.ErrBlobTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}

	first := stageForTest(t, store, "first", "first")
	key := first.ContentSHA256 + "-shared.bin"
	if _, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{WorkspaceID: "workspace-a", StageKey: first.BlobKey, BlobKey: key, ContentSHA256: first.ContentSHA256, Size: first.Size}); err != nil {
		t.Fatal(err)
	}
	path, err := store.path("workspace-a", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := stageForTest(t, store, "second", "first")
	if _, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{WorkspaceID: "workspace-a", StageKey: second.BlobKey, BlobKey: key, ContentSHA256: second.ContentSHA256, Size: second.Size}); !errors.Is(err, runtimefile.ErrBlobIdentityConflict) {
		t.Fatalf("identity conflict err=%v", err)
	}
	info, err := store.Stat(t.Context(), "workspace-a", key)
	if err != nil || info.ContentSHA256 == first.ContentSHA256 {
		t.Fatalf("conflicting destination was overwritten: info=%+v err=%v", info, err)
	}
}

func TestLocalStoreCommitIsIdempotentForIdenticalContent(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := stageForTest(t, store, "stage-1", "same")
	key := first.ContentSHA256 + "-same.bin"
	request := runtimefile.BlobCommitRequest{WorkspaceID: "workspace-a", StageKey: first.BlobKey, BlobKey: key, ContentSHA256: first.ContentSHA256, Size: first.Size}
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatalf("exact commit replay err=%v", err)
	}
	second := stageForTest(t, store, "stage-2", "same")
	request.StageKey = second.BlobKey
	if _, err := store.Commit(t.Context(), request); err != nil {
		t.Fatalf("idempotent commit err=%v", err)
	}
}

func TestLocalStoreRejectsInvalidStageIdentityAndSymbolicLink(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	staged := stageForTest(t, store, "stage", "content")
	key := staged.ContentSHA256 + "-content.bin"
	if _, err := store.Commit(t.Context(), runtimefile.BlobCommitRequest{WorkspaceID: "workspace-a", StageKey: key, BlobKey: key, ContentSHA256: staged.ContentSHA256, Size: staged.Size}); err == nil {
		t.Fatal("non-stage source identity was accepted")
	}
	external := root + "/external"
	if err := os.WriteFile(external, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	link, err := store.path("workspace-a", "linked.bin")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(t.Context(), "workspace-a", "linked.bin"); !errors.Is(err, runtimefile.ErrBlobIdentityConflict) {
		t.Fatalf("symbolic link open err=%v", err)
	}
}

func TestLocalStoreStageHonorsExactLimitAndContextCancellation(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{WorkspaceID: "workspace-a", StageID: "exact", Content: strings.NewReader("1234"), MaxBytes: 4}); err != nil || staged.Size != 4 {
		t.Fatalf("exact limit staged=%+v err=%v", staged, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancellingReader{cancel: cancel, content: []byte("1234")}
	if _, err := store.Stage(ctx, runtimefile.BlobStageRequest{WorkspaceID: "workspace-a", StageID: "cancelled", Content: reader, MaxBytes: 8}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stage err=%v", err)
	}
}

type cancellingReader struct {
	cancel  context.CancelFunc
	content []byte
	read    bool
}

func (r *cancellingReader) Read(target []byte) (int, error) {
	if r.read {
		return 0, context.Canceled
	}
	r.read = true
	target[0] = r.content[0]
	r.cancel()
	return 1, nil
}

func stageForTest(t *testing.T, store *LocalStore, stageID, content string) runtimefile.BlobInfo {
	t.Helper()
	staged, err := store.Stage(t.Context(), runtimefile.BlobStageRequest{WorkspaceID: "workspace-a", StageID: stageID, Content: strings.NewReader(content), MaxBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	return staged
}
