package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	artifactstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/artifact"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeArtifactCleanupDeletesOnlyElapsedBlobBackedContent(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "artifact-cleanup.db")})
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
	content := blobstore.LifecycleContentStore{Blobs: blobs}
	artifacts := artifactstore.NewStore(runtimeStore)
	now := time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)
	register := func(id, owner, kind string, expiresAt time.Time) sharedartifact.Artifact {
		t.Helper()
		info, putErr := content.PutImmutable(t.Context(), "workspace-a", id, []byte("content:"+id))
		if putErr != nil {
			t.Fatal(putErr)
		}
		value := sharedartifact.Artifact{
			ID: id, WorkspaceID: "workspace-a", Owner: owner, Kind: kind, IdempotencyKey: id,
			CreatedBy: "system", Filename: id + ".bin", MediaType: "application/octet-stream",
			ContentSHA256: info.SHA256, SizeBytes: info.Size, StorageReference: info.Reference,
			Status: sharedartifact.StatusAvailable, ExpiresAt: expiresAt, ScanStatus: sharedartifact.ScanNotRequired,
			Metadata: []byte(`{}`), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		}
		if _, created, registerErr := artifacts.Register(t.Context(), value); registerErr != nil || !created {
			t.Fatalf("register %s created=%v err=%v", id, created, registerErr)
		}
		return value
	}
	expiredAudit := register("audit-expired", sharedartifact.OwnerAudit, "export", now.Add(-time.Second))
	expiredOperation := register("operation-expired", sharedartifact.OwnerOperations, "result", now)
	expiredReport := register("report-expired", sharedartifact.OwnerReport, "export", now.Add(-time.Minute))
	expiredSubjectExport := register("subject-export-expired", sharedartifact.OwnerLifecycle, "subject_export", now.Add(-time.Minute))
	rejectedArchive := register("archive-rejected", sharedartifact.OwnerLifecycle, "archive", time.Time{})
	if changed, transitionErr := artifacts.Transition(t.Context(), rejectedArchive.WorkspaceID, rejectedArchive.ID, sharedartifact.StatusAvailable, sharedartifact.StatusRejected, rejectedArchive.ScanStatus, now); transitionErr != nil || !changed {
		t.Fatalf("reject archive changed=%v err=%v", changed, transitionErr)
	}
	futureAudit := register("audit-future", sharedartifact.OwnerAudit, "export", now.Add(time.Hour))

	cleanup, err := sharedartifact.NewCleanupService(artifacts, content)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reconcileRuntimeArtifactContent(t.Context(), cleanup, now, 10)
	if err != nil || result.Expired != 4 || result.Deleted != 5 {
		t.Fatalf("cleanup result=%#v err=%v", result, err)
	}
	for _, deleted := range []sharedartifact.Artifact{expiredAudit, expiredOperation, expiredReport, expiredSubjectExport, rejectedArchive} {
		persisted, found, loadErr := artifacts.ByID(t.Context(), deleted.WorkspaceID, deleted.ID)
		if loadErr != nil || !found || persisted.Status != sharedartifact.StatusDeleted {
			t.Fatalf("deleted artifact %s persisted=%#v found=%v err=%v", deleted.ID, persisted, found, loadErr)
		}
		if _, statErr := blobs.Stat(t.Context(), deleted.WorkspaceID, deleted.StorageReference); !errors.Is(statErr, runtimefile.ErrBlobNotFound) {
			t.Fatalf("deleted artifact %s content err=%v", deleted.ID, statErr)
		}
	}
	persistedFuture, found, err := artifacts.ByID(t.Context(), futureAudit.WorkspaceID, futureAudit.ID)
	if err != nil || !found || persistedFuture.Status != sharedartifact.StatusAvailable {
		t.Fatalf("future artifact persisted=%#v found=%v err=%v", persistedFuture, found, err)
	}
	if _, err = blobs.Stat(t.Context(), futureAudit.WorkspaceID, futureAudit.StorageReference); err != nil {
		t.Fatalf("future content removed: %v", err)
	}
}
