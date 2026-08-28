package frontendcapability

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openRuntimeStore(t *testing.T) *persistence.RuntimeStore {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "frontend-capability.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFrontendCapabilityStoreSharesVersionedManifest(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, second := NewFrontendCapabilityStore(store), NewFrontendCapabilityStore(store)
	var _ deploymentrepository.DeploymentFrontendCapabilityRepository = first
	manifest := deploymentmodel.FrontendCapabilityManifest{ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "1"}
	payload, _ := json.Marshal(manifest)
	record, err := first.Put(t.Context(), "workspace-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 1 {
		t.Fatalf("revision=%d", record.Revision)
	}
	manifest.FrontendVersion = "2"
	payload, _ = json.Marshal(manifest)
	record, err = second.Put(t.Context(), "workspace-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 2 {
		t.Fatalf("revision=%d", record.Revision)
	}
	visible, ok, err := first.Get(t.Context(), "workspace-a")
	var decoded deploymentmodel.FrontendCapabilityManifest
	_ = json.Unmarshal(visible.ManifestJSON, &decoded)
	if err != nil || !ok || visible.Revision != 2 || decoded.FrontendVersion != "2" {
		t.Fatalf("shared snapshot=%#v ok=%v err=%v", visible, ok, err)
	}
}

func TestFrontendCapabilityStoreWorkspaceIsolationContract(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewFrontendCapabilityStore(store)
	if _, err := repository.Put(t.Context(), "workspace-a", []byte(`{"frontend_version":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Put(t.Context(), "workspace-b", []byte(`{"frontend_version":"b"}`)); err != nil {
		t.Fatal(err)
	}
	workspaceA, ok, err := repository.Get(t.Context(), "workspace-a")
	if err != nil || !ok || workspaceA.WorkspaceID != "workspace-a" || string(workspaceA.ManifestJSON) != `{"frontend_version":"a"}` {
		t.Fatalf("workspace A state=%#v ok=%v err=%v", workspaceA, ok, err)
	}
	workspaceB, ok, err := repository.Get(t.Context(), "workspace-b")
	if err != nil || !ok || workspaceB.WorkspaceID != "workspace-b" || string(workspaceB.ManifestJSON) != `{"frontend_version":"b"}` {
		t.Fatalf("workspace B state=%#v ok=%v err=%v", workspaceB, ok, err)
	}
	if _, _, err := repository.Get(t.Context(), ""); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace get error=%v", err)
	}
	if _, err := repository.Put(t.Context(), "", []byte(`{}`)); !errors.Is(err, principalmodel.ErrWorkspaceIDRequired) {
		t.Fatalf("missing workspace put error=%v", err)
	}
}

func TestFrontendCapabilityStoreHonorsCancellation(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewFrontendCapabilityStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := repository.Get(ctx, "workspace-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("get err=%v", err)
	}
	if _, err := repository.Put(ctx, "workspace-a", []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatalf("put err=%v", err)
	}
}
