package artifact

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestArtifactStorePersistsMetadataWithoutDatabaseContent(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "artifacts.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(runtimeStore)
	now := time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)
	value := foundationartifact.Artifact{
		ID: "artifact-1", WorkspaceID: "workspace-a", Owner: foundationartifact.OwnerAudit, Kind: "export", IdempotencyKey: "export-1",
		CreatedBy: "user-1", Filename: "audit.csv", MediaType: "text/csv", ContentSHA256: "content-sha", SizeBytes: 42,
		StorageReference: "audit/workspace-a/artifact-1", Status: foundationartifact.StatusAvailable, ExpiresAt: now.Add(time.Hour), ScanStatus: foundationartifact.ScanNotRequired,
		DownloadTokenSHA256: "token-sha", AuthorizationScopeSHA256: "scope-sha", Metadata: []byte(`{"filters":{"event":"order.created"}}`), CreatedAt: now, UpdatedAt: now,
	}
	persisted, created, err := store.Register(t.Context(), value)
	if err != nil || !created || persisted.ID != value.ID {
		t.Fatalf("persisted=%#v created=%v err=%v", persisted, created, err)
	}
	replayed, created, err := store.Register(t.Context(), value)
	if err != nil || created || replayed.ID != value.ID {
		t.Fatalf("replayed=%#v created=%v err=%v", replayed, created, err)
	}
	elapsed := value
	elapsed.ID, elapsed.IdempotencyKey, elapsed.StorageReference = "artifact-elapsed", "export-elapsed", "audit/workspace-a/artifact-elapsed"
	elapsed.DownloadTokenSHA256, elapsed.ExpiresAt = "token-elapsed", now.Add(-time.Minute)
	if _, created, err = store.Register(t.Context(), elapsed); err != nil || !created {
		t.Fatalf("elapsed created=%v err=%v", created, err)
	}
	withoutExpiry := value
	withoutExpiry.ID, withoutExpiry.IdempotencyKey, withoutExpiry.StorageReference = "artifact-without-expiry", "export-without-expiry", "audit/workspace-a/artifact-without-expiry"
	withoutExpiry.DownloadTokenSHA256, withoutExpiry.ExpiresAt = "token-without-expiry", time.Time{}
	if _, created, err = store.Register(t.Context(), withoutExpiry); err != nil || !created {
		t.Fatalf("without expiry created=%v err=%v", created, err)
	}
	elapsedItems, err := store.List(t.Context(), "", foundationartifact.Query{
		Owner: foundationartifact.OwnerAudit, Kind: "export", Statuses: []foundationartifact.Status{foundationartifact.StatusAvailable},
		ExpiresAtOrBefore: now, Limit: 10,
	})
	if err != nil || len(elapsedItems) != 1 || elapsedItems[0].ID != elapsed.ID {
		t.Fatalf("elapsed artifacts=%#v err=%v", elapsedItems, err)
	}
	changed := value
	changed.ID, changed.ContentSHA256 = "artifact-conflict", "different"
	if _, _, err := store.Register(t.Context(), changed); !errors.Is(err, foundationartifact.ErrIdentityConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	byToken, found, err := store.ByDownloadTokenHash(t.Context(), value.WorkspaceID, value.DownloadTokenSHA256)
	if err != nil || !found || byToken.StorageReference != value.StorageReference || string(byToken.Metadata) != string(value.Metadata) {
		t.Fatalf("by token=%#v found=%v err=%v", byToken, found, err)
	}
	if _, found, err := store.ByID(t.Context(), "workspace-b", value.ID); err != nil || found {
		t.Fatalf("cross-workspace found=%v err=%v", found, err)
	}

	rows, err := runtimeStore.DB().QueryContext(t.Context(), `PRAGMA table_info('_artifacts')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "content" || name == "content_base64" || name == "payload" {
			t.Fatalf("artifact table contains binary payload column %q", name)
		}
	}
}

func TestArtifactStoreBindsResourcesAndFencesStatusTransitions(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "artifact-bindings.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(runtimeStore)
	now := time.Date(2026, 9, 22, 7, 0, 0, 0, time.UTC)
	value := foundationartifact.Artifact{ID: "artifact-1", WorkspaceID: "workspace-a", Owner: foundationartifact.OwnerReport, Kind: "export", IdempotencyKey: "report-1", CreatedBy: "user-1", Filename: "report.csv", MediaType: "text/csv", ContentSHA256: "sha", SizeBytes: 12, StorageReference: "reports/artifact-1", Status: foundationartifact.StatusPending, ScanStatus: foundationartifact.ScanPending, Metadata: []byte(`{}`), CreatedAt: now, UpdatedAt: now}
	if _, _, err := store.Register(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	binding := foundationartifact.Binding{ID: "binding-1", WorkspaceID: value.WorkspaceID, ArtifactID: value.ID, Owner: foundationartifact.OwnerReport, Kind: foundationartifact.BindingOperation, ResourceType: "operation", ResourceID: "operation-1", Metadata: []byte(`{"purpose":"download"}`), CreatedAt: now}
	if _, created, err := store.Bind(t.Context(), binding); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if _, created, err := store.Bind(t.Context(), binding); err != nil || created {
		t.Fatalf("replay created=%v err=%v", created, err)
	}
	bindings, err := store.Bindings(t.Context(), value.WorkspaceID, value.ID)
	if err != nil || len(bindings) != 1 || bindings[0].ResourceID != "operation-1" {
		t.Fatalf("bindings=%#v err=%v", bindings, err)
	}
	listed, err := store.List(t.Context(), value.WorkspaceID, foundationartifact.Query{
		Owner: foundationartifact.OwnerReport, Kind: "export", CreatedBy: []string{"user-1"},
		Binding: &foundationartifact.BindingQuery{Owner: foundationartifact.OwnerReport, Kind: foundationartifact.BindingOperation, ResourceType: "operation", ResourceID: "operation-1"}, Limit: 10,
	})
	if err != nil || len(listed) != 1 || listed[0].ID != value.ID {
		t.Fatalf("binding query artifacts=%#v err=%v", listed, err)
	}
	changed, err := store.Transition(t.Context(), value.WorkspaceID, value.ID, foundationartifact.StatusPending, foundationartifact.StatusAvailable, foundationartifact.ScanClean, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("transition changed=%v err=%v", changed, err)
	}
	changed, err = store.Transition(t.Context(), value.WorkspaceID, value.ID, foundationartifact.StatusPending, foundationartifact.StatusRejected, foundationartifact.ScanRejected, now.Add(2*time.Minute))
	if err != nil || changed {
		t.Fatalf("stale transition changed=%v err=%v", changed, err)
	}
	changed, err = store.Update(t.Context(), foundationartifact.Mutation{
		WorkspaceID: value.WorkspaceID, ID: value.ID, Owner: value.Owner, Kind: value.Kind,
		ExpectedStatus: foundationartifact.StatusAvailable, ExpectedScanStatus: foundationartifact.ScanClean,
		Status: foundationartifact.StatusRejected, ScanStatus: foundationartifact.ScanFailed,
		Metadata: []byte(`{"failure":"scanner unavailable"}`), UpdatedAt: now.Add(3 * time.Minute),
	})
	if err != nil || !changed {
		t.Fatalf("managed update changed=%v err=%v", changed, err)
	}
	persisted, found, err := store.ByID(t.Context(), value.WorkspaceID, value.ID)
	if err != nil || !found || persisted.Status != foundationartifact.StatusRejected || persisted.ScanStatus != foundationartifact.ScanFailed || string(persisted.Metadata) != `{"failure":"scanner unavailable"}` {
		t.Fatalf("persisted=%#v found=%v err=%v", persisted, found, err)
	}
}
