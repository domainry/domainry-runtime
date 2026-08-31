package runtime

import (
	"context"

	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifest "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func prepareRuntimeManifest(ctx context.Context, cfg config.Config) (manifestmodel.ManifestSchema, error) {
	return prepareRuntimeManifestWithCatalog(ctx, cfg, func(manifest manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error) {
		return manifest, nil
	})
}

type runtimeManifestCatalogAppender func(manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error)

func prepareRuntimeManifestWithCatalog(ctx context.Context, cfg config.Config, appendCatalog runtimeManifestCatalogAppender) (manifestmodel.ManifestSchema, error) {
	seedManifest, err := loadManifestSeedWithOptions(ctx, cfg.ManifestPath, cfg.AllowEmptyAuthoringManifest)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if !cfg.SkipManifestValidation && !(cfg.AllowEmptyAuthoringManifest && len(seedManifest.Objects) == 0) {
		validationManifest, validationErr := appendCatalog(seedManifest)
		if validationErr != nil {
			return manifestmodel.ManifestSchema{}, validationErr
		}
		if validationErr = manifest.ValidateManifest(validationManifest); validationErr != nil {
			return manifestmodel.ManifestSchema{}, validationErr
		}
	}
	return appschemaapplication.PrepareInstalledManifest(seedManifest), nil
}

// runtimeManifestHasNoBusinessObjects remains true after Runtime-owned system
// schemas have been generated for an empty direct-authoring manifest.
func runtimeManifestHasNoBusinessObjects(manifest manifestmodel.ManifestSchema) bool {
	for _, object := range manifest.Objects {
		if runtimeOwned, _ := object.Config["runtime_owned"].(bool); !runtimeOwned {
			return false
		}
	}
	return true
}
