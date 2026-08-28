package metadata

import metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

import (
	"context"
	"fmt"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"

	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func validateBusinessActionDefinitionIssues(action definitionmodel.ActionSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	return actionvalidation.ActionValidateDefinitionIssues(action)
}

func validateBusinessActionDefinitionIssuesWithObjects(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	return actionvalidation.ActionValidateDefinitionIssuesWithObjects(action, objects)
}

func ValidateStructuredMetadataDefinition(resourceType string, payload json.RawMessage) ([]metadatamodel.MetadataDefinitionValidationIssue, bool) {
	switch resourceType {
	case "action":
		var action definitionmodel.ActionSchema
		if err := json.Unmarshal(payload, &action); err != nil {
			return []metadatamodel.MetadataDefinitionValidationIssue{newMetadataDefinitionValidationIssue("backend.action.definition_invalid", "definition", "", "", nil)}, true
		}
		return validateBusinessActionDefinitionIssues(action), true
	case "connector":
		var connector integrationmodel.ConnectorSchema
		if err := json.Unmarshal(payload, &connector); err != nil {
			return []metadatamodel.MetadataDefinitionValidationIssue{newMetadataDefinitionValidationIssue("backend.integration.connector.definition_invalid", "definition", "", "", nil)}, true
		}
		return metadatavalidation.MetadataValidateConnectorDefinitionIssues(connector), true
	default:
		return nil, false
	}
}

// CanonicalizeMetadataCandidate is the sole pre-publication projection used by
// system drafts. It validates the complete composed candidate first, removes
// client JSON representation differences, and always derives compiler-owned
// Action effect sets on the server.
func (s *MetadataApplicationService) CanonicalizeMetadataCandidate(ctx context.Context, mutations []metadatamodel.MetadataDefinitionMutation) ([]metadatamodel.MetadataDefinitionMutation, error) {
	if err := s.ValidateMetadataCandidate(ctx, mutations); err != nil {
		return nil, err
	}
	canonical := make([]metadatamodel.MetadataDefinitionMutation, len(mutations))
	copy(canonical, mutations)
	for index := range canonical {
		mutation := &canonical[index]
		if mutation.Operation == "archive" || mutation.Operation == "delete" || mutation.Operation == "noop" {
			continue
		}
		var payload any
		if strings.TrimSpace(mutation.ResourceType) == "action" {
			var action definitionmodel.ActionSchema
			// ValidateMetadataCandidate has already decoded this exact mutation.
			_ = json.Unmarshal(mutation.Request.Payload, &action)
			action.EffectSet = nil
			payload = action
		} else {
			// Candidate validation guarantees syntactically valid JSON here.
			_ = json.Unmarshal(mutation.Request.Payload, &payload)
		}
		normalized, _ := json.Marshal(payload)
		mutation.Request.Payload = normalized
	}
	return canonical, nil
}

// validateInstalledActionAuthorization verifies Runtime-owned Action metadata.
// Identity owns grants and evaluates them through the SDK, so no role matrix is
// persisted or compared here.
func validateInstalledActionAuthorization(installed, persisted manifestmodel.ManifestSchema) error {
	persistedActions := make(map[string]definitionmodel.ActionSchema, len(persisted.Actions))
	for _, action := range persisted.Actions {
		persistedActions[strings.TrimSpace(action.Key)] = action
	}
	for _, action := range installed.Actions {
		actionKey := strings.TrimSpace(action.Key)
		requiredPermission := strings.TrimSpace(action.RequiresPermission)
		persistedAction, exists := persistedActions[actionKey]
		if !exists {
			return fmt.Errorf("Runtime metadata initialization is incomplete: installed Action %q was not persisted", actionKey)
		}
		if strings.TrimSpace(persistedAction.RequiresPermission) != requiredPermission {
			return fmt.Errorf(
				"Runtime metadata initialization is incomplete: persisted Action %q requires permission %q, want %q",
				actionKey, persistedAction.RequiresPermission, requiredPermission,
			)
		}
	}
	return nil
}
