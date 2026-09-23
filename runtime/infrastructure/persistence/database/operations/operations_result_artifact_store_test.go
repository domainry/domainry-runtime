package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsResultRegistrationFailureDeletesOnlyUnreferencedBlob(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-result-failure.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err = runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.NewLocalStore(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	registerErr := errors.New("register failed")
	base := runtimeStore.ArtifactStore()
	artifacts := &failingOperationsArtifactStore{Store: base, err: registerErr}
	store := NewOperationsResultArtifactStore(artifacts, blobs)
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	value := operationsrepository.OperationsResultArtifact{OperationID: "operation-failure", WorkspaceID: "workspace-a", CreatedBy: "operator-a", Content: []byte(`{"ok":true}`), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if _, err = store.PutOperationsResult(t.Context(), value); !errors.Is(err, registerErr) {
		t.Fatalf("put err=%v", err)
	}
	blobKey := operationsResultBlobKey(value)
	if _, err = blobs.Stat(t.Context(), value.WorkspaceID, blobKey); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("unregistered Blob remains: %v", err)
	}

	artifacts.commitThenFail = true
	reference, err := store.PutOperationsResult(t.Context(), value)
	if err != nil || reference.ID == "" {
		t.Fatalf("uncertain registration reference=%#v err=%v", reference, err)
	}
	if _, err = blobs.Stat(t.Context(), value.WorkspaceID, blobKey); err != nil {
		t.Fatalf("registered Blob was deleted: %v", err)
	}
}

type failingOperationsArtifactStore struct {
	foundationartifact.Store
	err            error
	commitThenFail bool
}

func (s *failingOperationsArtifactStore) Register(ctx context.Context, value foundationartifact.Artifact) (foundationartifact.Artifact, bool, error) {
	if s.commitThenFail {
		persisted, created, err := s.Store.Register(ctx, value)
		if err != nil {
			return persisted, created, err
		}
		return persisted, created, s.err
	}
	return foundationartifact.Artifact{}, false, s.err
}

func operationsResultBlobKey(value operationsrepository.OperationsResultArtifact) string {
	digest := sha256.Sum256(value.Content)
	identityDigest := sha256.Sum256([]byte(value.WorkspaceID + "\x00" + value.OperationID))
	return "operations-result-" + hex.EncodeToString(identityDigest[:16]) + "-" + hex.EncodeToString(digest[:]) + ".json"
}

func TestOperationsResultArtifactStorePersistsBytesAndOperationBinding(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-result.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.NewLocalStore(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewOperationsResultArtifactStore(runtimeStore.ArtifactStore(), blobs)
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	content := []byte(`{"items":"` + strings.Repeat("bounded-result", 2000) + `"}`)
	reference, err := store.PutOperationsResult(t.Context(), operationsrepository.OperationsResultArtifact{
		OperationID: "operation-1", WorkspaceID: "workspace-a", CreatedBy: "operator-a", Content: content, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedReference, err := store.GetOperationsResult(t.Context(), "workspace-a", "operation-1", reference.ID)
	if err != nil || !bytes.Equal(loaded, content) || loadedReference != reference {
		t.Fatalf("loaded bytes=%d reference=%#v err=%v", len(loaded), loadedReference, err)
	}
	var artifacts, bindings int
	if err := runtimeStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _artifacts WHERE workspace_id = ? AND owner = 'operations' AND kind = 'result'`, "workspace-a").Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if err := runtimeStore.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _artifact_bindings WHERE workspace_id = ? AND owner = 'operations' AND kind = 'operation' AND resource_id = ?`, "workspace-a", "operation-1").Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if artifacts != 1 || bindings != 1 {
		t.Fatalf("artifact rows=%d binding rows=%d", artifacts, bindings)
	}
}
