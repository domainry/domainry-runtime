package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestSeedAppliesRuntimeIdentityDefaults(t *testing.T) {
	path := writeManifestLoaderFixture(t, `{"schema_version":"2","objects":[{"key":"account","name":"Account"}]}`)
	manifest, err := loadManifestSeed(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TemplateID != generatedTemplateID || manifest.Version != generatedTemplateVersion {
		t.Fatalf("defaults = %q/%q", manifest.TemplateID, manifest.Version)
	}
}

func TestLoadManifestSeedAcceptsCurrentSchemaWithoutMigration(t *testing.T) {
	path := writeManifestLoaderFixture(t, `{"schema_version":"2","objects":[{"key":"account","name":"Account"}]}`)
	manifest, err := loadManifestSeed(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "2" {
		t.Fatalf("schema version = %q", manifest.SchemaVersion)
	}
}

func TestLoadManifestSeedRejectsUnreleasedLegacyContract(t *testing.T) {
	path := writeManifestLoaderFixture(t, `{"schema_version":"1","objects":[{"key":"account","name":"Account"}],"surfaces":[]}`)
	if _, err := loadManifestSeed(t.Context(), path); err == nil {
		t.Fatal("unreleased legacy manifest must fail")
	}
}

func TestLoadManifestSeedRejectsUnreadableInvalidAndObjectlessManifest(t *testing.T) {
	if _, err := loadManifestSeed(t.Context(), filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing manifest must fail")
	}
	if _, err := loadManifestSeed(t.Context(), writeManifestLoaderFixture(t, `{`)); err == nil {
		t.Fatal("invalid manifest must fail")
	}
	if _, err := loadManifestSeed(t.Context(), writeManifestLoaderFixture(t, `{"schema_version":"2"}`)); err == nil {
		t.Fatal("objectless manifest must fail")
	}
}

func TestLoadManifestSeedAllowsObjectlessOnlyForTrustedAuthoringBootstrap(t *testing.T) {
	path := writeManifestLoaderFixture(t, `{"schema_version":"2","template_id":"runtime-direct-authoring","version":"0.0.0","objects":[]}`)
	manifest, err := loadManifestSeedWithOptions(t.Context(), path, true)
	if err != nil || len(manifest.Objects) != 0 {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	if _, err := loadManifestSeed(t.Context(), path); err == nil {
		t.Fatal("ordinary Runtime startup accepted objectless manifest")
	}
}

func TestValueOrDefaultTrimsConfiguredValues(t *testing.T) {
	if got := valueOrDefault("  configured  ", "fallback"); got != "configured" {
		t.Fatalf("configured value = %q", got)
	}
	if got := valueOrDefault("  ", "fallback"); got != "fallback" {
		t.Fatalf("fallback value = %q", got)
	}
}

func writeManifestLoaderFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
