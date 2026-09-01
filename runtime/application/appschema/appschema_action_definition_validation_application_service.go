package appschema

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func validateBusinessActionDefinitionIssues(action definitionmodel.ActionSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	return actionvalidation.ActionValidateDefinitionIssues(action)
}

func validateBusinessActionDefinitionIssuesWithObjects(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	return actionvalidation.ActionValidateDefinitionIssuesWithObjects(action, objects)
}

func ValidateStructuredApplicationDefinition(resourceType string, payload json.RawMessage) ([]appschemamodel.ApplicationDefinitionValidationIssue, bool) {
	switch resourceType {
	case "action":
		var action definitionmodel.ActionSchema
		if err := json.Unmarshal(payload, &action); err != nil {
			return []appschemamodel.ApplicationDefinitionValidationIssue{newApplicationDefinitionValidationIssue("backend.action.definition_invalid", "definition", "", "", nil)}, true
		}
		return validateBusinessActionDefinitionIssues(action), true
	default:
		return nil, false
	}
}

// CanonicalizeMetadataCandidate is the sole pre-publication projection used by
// system drafts. It validates the complete composed candidate first, removes
// client JSON representation differences, and always derives compiler-owned
// Action effect sets on the server.
func (s *ApplicationSchemaApplicationService) CanonicalizeMetadataCandidate(ctx context.Context, mutations []appschemamodel.ApplicationDefinitionMutation) ([]appschemamodel.ApplicationDefinitionMutation, error) {
	if err := s.ValidateMetadataCandidate(ctx, mutations); err != nil {
		return nil, err
	}
	canonical := make([]appschemamodel.ApplicationDefinitionMutation, len(mutations))
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
		_, exists := persistedActions[actionKey]
		if !exists {
			return fmt.Errorf("Runtime metadata initialization is incomplete: installed Action %q was not persisted", actionKey)
		}
	}
	return nil
}
