package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

const generatedTemplateID = "runtime-source"
const generatedTemplateVersion = "0.0.0"

func loadManifestSeed(ctx context.Context, path string) (manifestmodel.ManifestSchema, error) {
	return loadManifestSeedWithOptions(ctx, path, false)
}

func loadManifestSeedWithOptions(_ context.Context, path string, allowEmptyObjects bool) (manifestmodel.ManifestSchema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	manifest, err := manifestmodel.DecodeManifest(raw)
	if err != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	manifest.TemplateID = valueOrDefault(manifest.TemplateID, generatedTemplateID)
	manifest.Version = valueOrDefault(manifest.Version, generatedTemplateVersion)
	if len(manifest.Objects) == 0 && !allowEmptyObjects {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("manifest %s has no objects", path)
	}
	return manifest, nil
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
