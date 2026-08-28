package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	preferencevalidation "github.com/domainry/domainry-runtime/runtime/domain/preference/validation"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	rulesetvalidation "github.com/domainry/domainry-runtime/runtime/domain/ruleset/validation"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"
)

// ValidateMetadataCandidate composes every mutation over the current active
// graph before persistence. This is the system-draft validation boundary:
// references created, replaced, or removed in the same draft are evaluated as
// one candidate rather than against the old Runtime one item at a time.
func (s *MetadataApplicationService) ValidateMetadataCandidate(ctx context.Context, mutations []metadatamodel.MetadataDefinitionMutation) error {
	return s.validateMetadataCandidateWithConnectorCatalog(ctx, mutations, nil)
}

// ValidateCurrentRuntimeDefinitions reuses the Change Plan candidate boundary
// for the active graph while resolving connector references against the live
// Runtime catalog, matching bootstrap and manifest validation semantics.
func (s *MetadataApplicationService) ValidateCurrentRuntimeDefinitions(ctx context.Context, connectorCatalog []integrationmodel.ConnectorSchema) error {
	return s.validateMetadataCandidateWithConnectorCatalog(ctx, nil, connectorCatalog)
}

func (s *MetadataApplicationService) validateMetadataCandidateWithConnectorCatalog(ctx context.Context, mutations []metadatamodel.MetadataDefinitionMutation, connectorCatalog []integrationmodel.ConnectorSchema) error {
	if s == nil || s.repository == nil {
		return badRequest("backend.change_plan.candidate_invalid", "diagnostic", "metadata repository is unavailable")
	}
	candidate, err := s.repository.LoadManifest(ctx, metadataInstallationScope("validate composed metadata candidate"))
	if err != nil {
		return wrapMetadataError(err)
	}
	for _, mutation := range mutations {
		if err := applyMetadataCandidateMutation(&candidate, mutation); err != nil {
			return badRequest("backend.change_plan.candidate_invalid", "resource_type", mutation.ResourceType, "resource_key", mutation.ResourceKey, "diagnostic", err.Error())
		}
	}
	if err := manifestvalidation.ValidateRuntimeDefinitionGraphWithConnectorCatalog(candidate, connectorCatalog); err != nil {
		return badRequest("backend.change_plan.candidate_invalid", "diagnostic", err.Error())
	}
	for _, connector := range candidate.Integrations.Connectors {
		if err := metadatavalidation.MetadataValidateConnectorDefinition(connector); err != nil {
			return badRequest("backend.change_plan.candidate_invalid", "resource_type", "connector", "resource_key", connector.Key, "diagnostic", err.Error())
		}
	}
	for _, action := range candidate.Actions {
		if issues := validateBusinessActionDefinitionIssuesWithObjects(action, candidate.Objects); len(issues) > 0 {
			return badRequest("backend.change_plan.candidate_invalid", "resource_type", "action", "resource_key", action.Key, "diagnostic", issues[0].ErrorCode+":"+issues[0].FieldPath)
		}
	}
	if err := s.validateMetadataCandidateSchedulers(ctx, candidate, mutations); err != nil {
		return badRequest("backend.change_plan.candidate_invalid", "diagnostic", err.Error())
	}
	return nil
}

func (s *MetadataApplicationService) validateMetadataCandidateSchedulers(ctx context.Context, candidate manifestmodel.ManifestSchema, mutations []metadatamodel.MetadataDefinitionMutation) error {
	installation := metadataInstallationScope("validate composed scheduler definitions")
	definitions := map[string]map[string]any{}
	active, err := s.repository.ListDefinitions(ctx, installation, "scheduler")
	if err != nil {
		return fmt.Errorf("load candidate schedulers: %w", err)
	}
	for _, definition := range active {
		var payload map[string]any
		if err := json.Unmarshal(definition.Payload, &payload); err != nil {
			return fmt.Errorf("decode scheduler %s: %w", definition.ResourceKey, err)
		}
		definitions[definition.ResourceKey] = payload
	}
	for _, mutation := range mutations {
		if strings.TrimSpace(mutation.ResourceType) != "scheduler" {
			continue
		}
		key := strings.TrimSpace(mutation.ResourceKey)
		if mutation.Operation == "archive" || mutation.Operation == "delete" {
			delete(definitions, key)
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(mutation.Request.Payload, &payload); err != nil {
			return fmt.Errorf("decode scheduler %s: %w", key, err)
		}
		if strings.TrimSpace(fmt.Sprint(payload["key"])) != key {
			return fmt.Errorf("scheduler resource key mismatch: expected %s, got %s", key, strings.TrimSpace(fmt.Sprint(payload["key"])))
		}
		definitions[key] = payload
	}
	workflows := map[string]bool{}
	for _, workflow := range candidate.Workflows {
		workflows[strings.TrimSpace(workflow.Key)] = true
	}
	reports := map[string]bool{}
	for _, report := range candidate.Reports {
		reports[strings.TrimSpace(report.Key)] = true
	}
	for key, definition := range definitions {
		if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, definition); err != nil {
			return fmt.Errorf("scheduler %s is invalid: %w", key, err)
		}
		targetType := strings.ToLower(strings.TrimSpace(fmt.Sprint(definition["target_type"])))
		targetKey := strings.TrimSpace(fmt.Sprint(definition["target_key"]))
		switch targetType {
		case "workflow":
			if targetKey != "scheduled:*" {
				workflowKey := strings.TrimPrefix(targetKey, "scheduled:")
				if !workflows[workflowKey] {
					return fmt.Errorf("scheduler %s references missing workflow %s at target_key", key, workflowKey)
				}
			}
		case "report_export", "report_snapshot_refresh":
			if !reports[targetKey] {
				return fmt.Errorf("scheduler %s references missing report %s at target_key", key, targetKey)
			}
		}
	}
	return nil
}

func applyMetadataCandidateMutation(candidate *manifestmodel.ManifestSchema, mutation metadatamodel.MetadataDefinitionMutation) error {
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
	case "view":
		return candidateApplySlice(&candidate.Views, resourceKey, payload, remove, func(value definitionmodel.ViewSchema) string { return value.Key })
	case "action":
		return candidateApplySlice(&candidate.Actions, resourceKey, payload, remove, func(value definitionmodel.ActionSchema) string { return value.Key })
	case "workflow":
		return candidateApplySlice(&candidate.Workflows, resourceKey, payload, remove, func(value definitionmodel.WorkflowSchema) string { return value.Key })
	case "scheduler":
		// Scheduler definitions are versioned Metadata resources but are not part
		// of the Blueprint/Manifest envelope. They are composed and validated as
		// one candidate in validateMetadataCandidateSchedulers.
		return nil
	case "automation_rule":
		return candidateApplySlice(&candidate.AutomationRules, resourceKey, payload, remove, func(value automationmodel.AutomationRuleSchema) string { return value.Key })
	case "dictionary":
		return candidateApplySlice(&candidate.Dictionaries, resourceKey, payload, remove, func(value metadatamodel.DictionarySchema) string { return value.Key })
	case "connector":
		return candidateApplySlice(&candidate.Integrations.Connectors, resourceKey, payload, remove, func(value integrationmodel.ConnectorSchema) string { return value.Key })
	case "integration_event_mapping":
		return candidateApplySlice(&candidate.Integrations.EventMappings, resourceKey, payload, remove, func(value integrationmodel.IntegrationEventMappingSchema) string { return value.Key })
	case "report":
		return candidateApplySlice(&candidate.Reports, resourceKey, payload, remove, func(value reportmodel.ReportSchema) string { return value.Key })
	case "operation_state_example":
		return candidateApplySlice(&candidate.OperationStateExamples, resourceKey, payload, remove, func(value reportmodel.ReportOperationStateExampleSchema) string { return value.Key })
	case "sensitive_field_policy":
		return candidateApplySlice(&candidate.SensitiveFieldPolicies, resourceKey, payload, remove, func(value reportmodel.ReportSensitiveFieldPolicySchema) string { return value.Key })
	case "report_export_control":
		return candidateApplySlice(&candidate.ReportExportControls, resourceKey, payload, remove, func(value reportmodel.ReportExportControlSchema) string { return value.Key })
	case "entrypoint":
		return candidateApplySlice(&candidate.EntryPoints, resourceKey, payload, remove, func(value definitionmodel.EntryPointSchema) string { return value.Key })
	case "skill":
		return candidateApplySlice(&candidate.Skills, resourceKey, payload, remove, func(value agentmodel.SkillSchema) string { return value.Key })
	case "agent":
		return candidateApplySlice(&candidate.Agents, resourceKey, payload, remove, func(value agentmodel.AgentSchema) string { return value.Key })
	case "identity_profile_binding":
		return candidateApplySlice(&candidate.IdentityProfileExtensions, resourceKey, payload, remove, func(value profilebindingmodel.Binding) string { return value.ObjectKey })
	case "preference":
		if remove {
			return nil
		}
		_, err := preferencevalidation.DecodeWorkspacePreferenceDefinition(resourceKey, payload)
		return err
	case "rule_set":
		if remove {
			return nil
		}
		_, err := rulesetvalidation.DecodeRuleSetDefinition(resourceKey, payload)
		return err
	default:
		return fmt.Errorf("unsupported candidate resource type %q", resourceType)
	}
	return nil
}

func applyCandidateField(candidate *manifestmodel.ManifestSchema, mutation metadatamodel.MetadataDefinitionMutation, remove bool) error {
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

func applyCandidateValidation(candidate *manifestmodel.ManifestSchema, mutation metadatamodel.MetadataDefinitionMutation, remove bool) error {
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

func candidateObjectMemberKey(mutation metadatamodel.MetadataDefinitionMutation) (string, string) {
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
