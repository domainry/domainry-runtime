package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

// addIntegrationOwnerValidationCatalog lets application metadata bind Actions,
// Automations, and Workflows to Integration-owned connector definitions. The
// complete definition crosses the owner boundary as JSON through the SDK;
// Runtime keeps only a transient validation projection.
func addIntegrationOwnerValidationCatalog(ctx context.Context, manifest manifestmodel.ManifestSchema, catalog integrationsdk.Catalog) (manifestmodel.ManifestSchema, error) {
	if catalog == nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("Integration Catalog is required")
	}
	if !manifestNeedsIntegrationOwnerValidationCatalog(manifest) {
		manifest.Integrations.Connectors = nil
		return manifest, nil
	}
	definitions, err := catalog.ListConnectorDefinitions(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("list Integration connector definitions: %w", err)
	}
	validationCatalog := make([]connectormodel.ConnectorSchema, 0, len(definitions))
	for _, definition := range definitions {
		if len(definition.Definition) == 0 || string(definition.Definition) == "null" {
			continue
		}
		var connector connectormodel.ConnectorSchema
		if err := json.Unmarshal(definition.Definition, &connector); err != nil {
			return manifestmodel.ManifestSchema{}, fmt.Errorf("decode Integration connector definition %q: %w", definition.Key, err)
		}
		if connector.Key == "" {
			connector.Key = definition.Key
		}
		if connector.Key != definition.Key {
			return manifestmodel.ManifestSchema{}, fmt.Errorf("Integration connector definition %q projects key %q", definition.Key, connector.Key)
		}
		validationCatalog = append(validationCatalog, connector)
	}
	return replaceIntegrationConnectorValidationProjection(manifest, validationCatalog, nil)
}

func manifestNeedsIntegrationOwnerValidationCatalog(manifest manifestmodel.ManifestSchema) bool {
	if len(manifest.Integrations.Connectors) != 0 || len(manifest.Integrations.Connections) != 0 {
		return true
	}
	for _, rule := range manifest.AutomationRules {
		for _, instruction := range rule.Instructions {
			if strings.TrimSpace(instruction.ConnectorKey) != "" {
				return true
			}
			switch strings.TrimSpace(instruction.Type) {
			case "integration_call", "reserve":
				return true
			}
		}
	}
	return false
}

func replaceIntegrationConnectorValidationProjection(manifest manifestmodel.ManifestSchema, validationCatalog []connectormodel.ConnectorSchema, catalogErr error) (manifestmodel.ManifestSchema, error) {
	if catalogErr != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("load Integration Connector validation projection: %w", catalogErr)
	}
	manifest.Integrations.Connectors = append([]connectormodel.ConnectorSchema(nil), validationCatalog...)
	return manifest, nil
}
