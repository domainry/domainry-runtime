package runtime

import (
	"context"

	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifest "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func prepareRuntimeManifest(ctx context.Context, cfg config.Config) (manifestmodel.ManifestSchema, error) {
	return prepareRuntimeManifestWithCatalog(ctx, cfg, addRuntimeConnectorValidationCatalog)
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
