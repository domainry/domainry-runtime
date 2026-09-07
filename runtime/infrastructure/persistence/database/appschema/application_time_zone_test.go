package appschema

import (
	"path/filepath"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestApplicationTimeZonePersistsAndInvalidatesMetadataRevision(t *testing.T) {
	store := openStoreForMetadataTest(t)
	repository := store.ApplicationSchemaStore
	manifest := manifestmodel.ManifestSchema{TemplateID: "timezone", Version: "1", TimeZone: "Asia/Tokyo", Objects: []definitionmodel.ObjectSchema{{Key: "sale", Name: "Sale"}}}
	lastRevision := ""
	for _, zone := range []string{"Asia/Tokyo", "America/New_York", ""} {
		manifest.TimeZone = zone
		if err := repository.SyncManifestProjection(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
			t.Fatal(err)
		}
		loaded, err := repository.LoadManifest(t.Context(), metadataTestInstallationScope())
		if err != nil {
			t.Fatal(err)
		}
		if loaded.TimeZone != manifest.EffectiveTimeZone() {
			t.Fatalf("persisted zone=%q expected=%q", loaded.TimeZone, manifest.EffectiveTimeZone())
		}
		revision, err := repository.SnapshotRevision(t.Context(), metadataTestInstallationScope())
		if err != nil {
			t.Fatal(err)
		}
		if revision == "" || revision == lastRevision {
			t.Fatalf("time-zone-only change did not invalidate revision: %q", revision)
		}
		lastRevision = revision
	}
	manifest.TimeZone = "Not/AZone"
	if err := repository.SyncManifestProjection(t.Context(), metadataTestInstallationScope(), manifest); err == nil {
		t.Fatal("invalid zone persisted")
	}
	loaded, err := repository.LoadManifest(t.Context(), metadataTestInstallationScope())
	if err != nil || loaded.TimeZone != "UTC" {
		t.Fatalf("rejected mutation changed header: %q %v", loaded.TimeZone, err)
	}
}

func TestApplicationTimeZoneHeaderSurvivesDatabaseReopen(t *testing.T) {
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "time-zone.db")}
	binding := newMetadataBindingStub()
	open := func() *database.RuntimeStore {
		t.Helper()
		store, err := database.OpenContext(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if err := store.BindMetadata(binding); err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		return store
	}
	first := open()
	manifest := manifestmodel.ManifestSchema{TemplateID: "timezone", Version: "1", TimeZone: "Asia/Tokyo", Objects: []definitionmodel.ObjectSchema{{Key: "sale", Name: "Sale"}}}
	if err := NewApplicationSchemaStore(first).SyncManifestProjection(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := open()
	loaded, err := NewApplicationSchemaStore(restarted).LoadManifest(t.Context(), metadataTestInstallationScope())
	if err != nil || loaded.TimeZone != "Asia/Tokyo" {
		t.Fatalf("reopened header zone=%q error=%v", loaded.TimeZone, err)
	}
}
