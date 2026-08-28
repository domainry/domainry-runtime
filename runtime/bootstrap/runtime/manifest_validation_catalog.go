package runtime

import (
	"fmt"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestprojection "github.com/domainry/domainry-runtime/runtime/domain/manifest/projection"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	catalog "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
)

// addRuntimeConnectorValidationCatalog lets domain metadata bind Actions,
// Automations, and Workflows to Runtime-owned Connector contracts without
// copying those contracts into the user's manifest. Manifest definitions keep
// precedence only for legacy fixtures; Runtime registration still owns the
// effective builtin contract.
func addRuntimeConnectorValidationCatalog(manifest manifestmodel.ManifestSchema) (manifestmodel.ManifestSchema, error) {
	validationCatalog, err := catalog.Builtin()
	return mergeRuntimeConnectorValidationCatalog(manifest, validationCatalog, err)
}

func mergeRuntimeConnectorValidationCatalog(manifest manifestmodel.ManifestSchema, validationCatalog []integrationmodel.ConnectorSchema, catalogErr error) (manifestmodel.ManifestSchema, error) {
	if catalogErr != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("load Runtime Connector validation catalog: %w", catalogErr)
	}
	return manifestprojection.MergeConnectorValidationCatalog(manifest, validationCatalog), nil
}
