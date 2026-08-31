package appschema

import (
	"context"
	"encoding/json"
	"fmt"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
)

// ValidateMetadataCandidate composes every mutation over the current active
// graph before persistence. This is the system-draft validation boundary:
// references created, replaced, or removed in the same draft are evaluated as
// one candidate rather than against the old Runtime one item at a time.
func (s *ApplicationSchemaApplicationService) ValidateMetadataCandidate(ctx context.Context, mutations []appschemamodel.ApplicationDefinitionMutation) error {
	return s.validateMetadataCandidateWithConnectorCatalog(ctx, mutations, nil)
}

// ValidateCurrentRuntimeDefinitions reuses the Change Plan candidate boundary
// for the active graph while resolving connector references against the live
// Runtime catalog, matching bootstrap and manifest validation semantics.
func (s *ApplicationSchemaApplicationService) ValidateCurrentRuntimeDefinitions(ctx context.Context, connectorCatalog []connectormodel.ConnectorSchema) error {
	return s.validateMetadataCandidateWithConnectorCatalog(ctx, nil, connectorCatalog)
}

func (s *ApplicationSchemaApplicationService) validateMetadataCandidateWithConnectorCatalog(ctx context.Context, mutations []appschemamodel.ApplicationDefinitionMutation, connectorCatalog []connectormodel.ConnectorSchema) error {
	if s == nil || s.repository == nil {
		return badRequest("backend.metadata.candidate_invalid", "diagnostic", "metadata repository is unavailable")
	}
	candidate, err := s.repository.LoadManifest(ctx, metadataInstallationScope("validate composed metadata candidate"))
	if err != nil {
		return wrapMetadataError(err)
	}
	for _, mutation := range mutations {
		if err := applyMetadataCandidateMutation(&candidate, mutation); err != nil {
			return badRequest("backend.metadata.candidate_invalid", "resource_type", mutation.ResourceType, "resource_key", mutation.ResourceKey, "diagnostic", err.Error())
		}
	}
	if err := manifestvalidation.ValidateRuntimeDefinitionGraphWithConnectorCatalog(candidate, connectorCatalog); err != nil {
		return badRequest("backend.metadata.candidate_invalid", "diagnostic", err.Error())
	}
	for _, connector := range candidate.Integrations.Connectors {
		if err := appschemavalidation.ApplicationSchemaValidateConnectorDefinition(connector); err != nil {
			return badRequest("backend.metadata.candidate_invalid", "resource_type", "connector", "resource_key", connector.Key, "diagnostic", err.Error())
		}
	}
	for _, action := range candidate.Actions {
		if issues := validateBusinessActionDefinitionIssuesWithObjects(action, candidate.Objects); len(issues) > 0 {
			return badRequest("backend.metadata.candidate_invalid", "resource_type", "action", "resource_key", action.Key, "diagnostic", issues[0].ErrorCode+":"+issues[0].FieldPath)
		}
	}
	return nil
}

func applyMetadataCandidateMutation(candidate *manifestmodel.ManifestSchema, mutation appschemamodel.ApplicationDefinitionMutation) error {
	resourceType := strings.TrimSpace(mutation.ResourceType)
	resourceKey := strings.TrimSpace(mutation.ResourceKey)
	remove := mutation.Operation == "archive" || mutation.Operation == "delete"
	if mutation.Operation == "noop" {
		return nil
	}
	if !remove && mutation.Operation != "create" && mutation.Operation != "update" {
		return fmt.Errorf("unsupported operation %q", mutation.Operation)
	}
	payload := mutation.Request.Payload
	switch resourceType {
	case "object":
		if remove {
			candidate.Objects = candidateRemove(candidate.Objects, resourceKey, func(value definitionmodel.ObjectSchema) string { return value.Key })
			return nil
		}
		value, err := candidateDecode[definitionmodel.ObjectSchema](payload, resourceKey, func(value definitionmodel.ObjectSchema) string { return value.Key })
		if err != nil {
			return err
		}
		for _, current := range candidate.Objects {
			if current.Key == resourceKey {
				value.Fields, value.Validations = current.Fields, current.Validations
				break
			}
		}
		candidate.Objects = candidateReplace(candidate.Objects, resourceKey, value, func(value definitionmodel.ObjectSchema) string { return value.Key })
	case "field":
		return applyCandidateField(candidate, mutation, remove)
	case "validation":
		return applyCandidateValidation(candidate, mutation, remove)
	case "action":
		return candidateApplySlice(&candidate.Actions, resourceKey, payload, remove, func(value definitionmodel.ActionSchema) string { return value.Key })
	case "workflow":
		return candidateApplySlice(&candidate.Workflows, resourceKey, payload, remove, func(value definitionmodel.WorkflowSchema) string { return value.Key })
	case "automation_rule":
		return candidateApplySlice(&candidate.AutomationRules, resourceKey, payload, remove, func(value automationmodel.AutomationRuleSchema) string { return value.Key })
	case "dictionary":
		return candidateApplySlice(&candidate.Dictionaries, resourceKey, payload, remove, func(value appschemamodel.DictionarySchema) string { return value.Key })
	case "integration_event_mapping":
		return candidateApplySlice(&candidate.Integrations.EventMappings, resourceKey, payload, remove, func(value connectormodel.IntegrationEventMappingSchema) string { return value.Key })
	case "skill":
		return candidateApplySlice(&candidate.Skills, resourceKey, payload, remove, func(value agentsdk.SkillSchema) string { return value.Key })
	case "agent":
		return candidateApplySlice(&candidate.Agents, resourceKey, payload, remove, func(value agentsdk.AgentSchema) string { return value.Key })
	default:
		return fmt.Errorf("unsupported candidate resource type %q", resourceType)
	}
	return nil
}

func applyCandidateField(candidate *manifestmodel.ManifestSchema, mutation appschemamodel.ApplicationDefinitionMutation, remove bool) error {
	objectKey, fieldKey := candidateObjectMemberKey(mutation)
	index := candidateObjectIndex(candidate.Objects, objectKey)
	if index < 0 {
		return fmt.Errorf("field %s references unknown object %s", mutation.ResourceKey, objectKey)
	}
	if remove {
		candidate.Objects[index].Fields = candidateRemove(candidate.Objects[index].Fields, fieldKey, func(value definitionmodel.FieldSchema) string { return value.Key })
		return nil
	}
	value, err := candidateDecode[definitionmodel.FieldSchema](mutation.Request.Payload, fieldKey, func(value definitionmodel.FieldSchema) string { return value.Key })
	if err != nil {
		return err
	}
	candidate.Objects[index].Fields = candidateReplace(candidate.Objects[index].Fields, fieldKey, value, func(value definitionmodel.FieldSchema) string { return value.Key })
	return nil
}

func applyCandidateValidation(candidate *manifestmodel.ManifestSchema, mutation appschemamodel.ApplicationDefinitionMutation, remove bool) error {
	objectKey := strings.TrimSpace(mutation.Request.ObjectKey)
	if objectKey == "" {
		var value definitionmodel.ValidationSchema
		_ = json.Unmarshal(mutation.Request.Payload, &value)
		objectKey = strings.TrimSpace(value.ObjectKey)
	}
	index := candidateObjectIndex(candidate.Objects, objectKey)
	if index < 0 {
		return fmt.Errorf("validation %s references unknown object %s", mutation.ResourceKey, objectKey)
	}
	if remove {
		candidate.Objects[index].Validations = candidateRemove(candidate.Objects[index].Validations, mutation.ResourceKey, func(value definitionmodel.ValidationSchema) string { return value.Key })
		return nil
	}
	value, err := candidateDecode[definitionmodel.ValidationSchema](mutation.Request.Payload, mutation.ResourceKey, func(value definitionmodel.ValidationSchema) string { return value.Key })
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.ObjectKey) != objectKey {
		return fmt.Errorf("validation object key mismatch: expected %s, got %s", objectKey, value.ObjectKey)
	}
	candidate.Objects[index].Validations = candidateReplace(candidate.Objects[index].Validations, mutation.ResourceKey, value, func(value definitionmodel.ValidationSchema) string { return value.Key })
	return nil
}

func candidateObjectMemberKey(mutation appschemamodel.ApplicationDefinitionMutation) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(mutation.ResourceKey), ".", 2)
	objectKey := strings.TrimSpace(mutation.Request.ObjectKey)
	fieldKey := ""
	if len(parts) == 2 {
		if objectKey == "" {
			objectKey = parts[0]
		}
		fieldKey = parts[1]
	}
	return objectKey, fieldKey
}
