package metadata

import (
	"bytes"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	preferencevalidation "github.com/domainry/domainry-runtime/runtime/domain/preference/validation"
	rulesetvalidation "github.com/domainry/domainry-runtime/runtime/domain/ruleset/validation"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"github.com/domainry/domainry-foundation/apperror"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func candidateObjectIndex(objects []definitionmodel.ObjectSchema, key string) int {
	for index := range objects {
		if strings.TrimSpace(objects[index].Key) == strings.TrimSpace(key) {
			return index
		}
	}
	return -1
}

func candidateApplySlice[T any](values *[]T, key string, payload json.RawMessage, remove bool, keyOf func(T) string) error {
	if remove {
		*values = candidateRemove(*values, key, keyOf)
		return nil
	}
	value, err := candidateDecode[T](payload, key, keyOf)
	if err != nil {
		return err
	}
	*values = candidateReplace(*values, key, value, keyOf)
	return nil
}

func candidateDecode[T any](payload json.RawMessage, expectedKey string, keyOf func(T) string) (T, error) {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, err
	}
	actual := strings.TrimSpace(keyOf(value))
	if actual != strings.TrimSpace(expectedKey) {
		return value, fmt.Errorf("resource key mismatch: expected %s, got %s", expectedKey, actual)
	}
	return value, nil
}

func candidateReplace[T any](values []T, key string, value T, keyOf func(T) string) []T {
	out := make([]T, 0, len(values)+1)
	replaced := false
	for _, current := range values {
		if strings.TrimSpace(keyOf(current)) == strings.TrimSpace(key) {
			if !replaced {
				out = append(out, value)
				replaced = true
			}
			continue
		}
		out = append(out, current)
	}
	if !replaced {
		out = append(out, value)
	}
	return out
}

func candidateRemove[T any](values []T, key string, keyOf func(T) string) []T {
	out := make([]T, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(keyOf(value)) != strings.TrimSpace(key) {
			out = append(out, value)
		}
	}
	return out
}

func (s *MetadataApplicationService) ValidateMetadataDefinition(ctx context.Context, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest, principal principalmodel.Principal) (metadatamodel.MetadataDefinitionValidationResult, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return metadatamodel.MetadataDefinitionValidationResult{}, err
	}
	return metadatavalidation.MetadataValidateDefinitionRequest(ctx, resourceType, resourceKey, req.Payload, principal,
		func(ctx context.Context, resourceType, resourceKey string, payload json.RawMessage) (json.RawMessage, []metadatamodel.MetadataDefinitionValidationIssue, error) {
			req.Payload = payload
			return s.ValidateMetadataDefinitionRequestPayload(ctx, resourceType, resourceKey, req)
		})
}

func (s *MetadataApplicationService) ValidateMetadataDefinitionRequestPayload(ctx context.Context, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest) (json.RawMessage, []metadatamodel.MetadataDefinitionValidationIssue, error) {
	if resourceType == "object" {
		normalized, err := metadatavalidation.MetadataValidateObjectDefinition(resourceKey, req.Payload)
		return normalized, nil, err
	}
	if resourceType == "view" {
		normalized, err := metadatavalidation.MetadataValidateViewDefinition(resourceKey, req.Payload, s.runtime.Schema().Objects)
		return normalized, nil, err
	}
	if resourceType == "dictionary" {
		if err := metadatavalidation.MetadataValidateDictionaryDefinition(resourceKey, req.Payload); err != nil {
			return nil, nil, err
		}
	}
	if resourceType == "preference" {
		preference, err := preferencevalidation.DecodeWorkspacePreferenceDefinition(resourceKey, req.Payload)
		if err != nil {
			return nil, nil, apperror.FromError(apperror.KindBadRequest, err)
		}
		normalized, _ := json.Marshal(preference)
		return normalized, nil, nil
	}
	if resourceType == "rule_set" {
		ruleSet, err := rulesetvalidation.DecodeRuleSetDefinition(resourceKey, req.Payload)
		if err != nil {
			return nil, nil, apperror.FromError(apperror.KindBadRequest, err)
		}
		normalized, _ := json.Marshal(ruleSet)
		return normalized, nil, nil
	}
	if resourceType == "action" {
		action, err := decodeActionDefinitionPayload(req.Payload)
		if err != nil {
			return nil, []metadatamodel.MetadataDefinitionValidationIssue{newMetadataDefinitionValidationIssue("backend.action.definition_invalid", "definition", "", "", nil)}, nil
		}
		if issues := validateBusinessActionDefinitionIssuesWithObjects(action, s.runtime.Schema().Objects); len(issues) > 0 {
			return nil, issues, nil
		}
		action.EffectSet = nil
		normalized, err := json.Marshal(action)
		return normalized, nil, err
	}
	if issues, handled := ValidateStructuredMetadataDefinition(resourceType, req.Payload); handled {
		if len(issues) > 0 {
			return nil, issues, nil
		}
		return req.Payload, nil, nil
	}
	if resourceType == "report" {
		var report reportmodel.ReportSchema
		if err := json.Unmarshal(req.Payload, &report); err != nil {
			return nil, nil, badRequest("backend.report.definition_invalid")
		}
		if issues := s.validateReportDefinitionIssues(ctx, report); len(issues) > 0 {
			return nil, issues, nil
		}
		return req.Payload, nil, nil
	}
	normalized, err := s.ValidateMetadataDefinitionPayload(ctx, resourceType, req)
	if err != nil {
		return nil, nil, err
	}
	return normalized.Payload, nil, nil
}

func (s *MetadataApplicationService) ValidateMetadataDefinitionPayload(ctx context.Context, resourceType string, req metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
	switch resourceType {
	case "field":
		return s.normalizeAndValidateFieldMetadataMutation(ctx, req)
	case "automation_rule":
		var rule automationmodel.AutomationRuleSchema
		if err := json.Unmarshal(req.Payload, &rule); err != nil {
			return req, badRequest("backend.automation.definition_invalid")
		}
		return req, s.ValidateAutomationRuleDefinition(ctx, rule)
	case "preference":
		preference, err := preferencevalidation.DecodeWorkspacePreferenceDefinition("", req.Payload)
		if err != nil {
			return req, apperror.FromError(apperror.KindBadRequest, err)
		}
		normalized, _ := json.Marshal(preference)
		req.Payload = normalized
		return req, nil
	case "rule_set":
		ruleSet, err := rulesetvalidation.DecodeRuleSetDefinition("", req.Payload)
		if err != nil {
			return req, apperror.FromError(apperror.KindBadRequest, err)
		}
		normalized, _ := json.Marshal(ruleSet)
		req.Payload = normalized
		return req, nil
	case "identity_profile_binding":
		var binding profilebindingmodel.Binding
		if err := json.Unmarshal(req.Payload, &binding); err != nil {
			return req, badRequest("backend.identity.profile_binding_invalid")
		}
		bindings := make([]profilebindingmodel.Binding, 0, len(s.runtime.Schema().IdentityProfileExtensions)+1)
		for _, current := range s.runtime.Schema().IdentityProfileExtensions {
			if current.ObjectKey != binding.ObjectKey {
				bindings = append(bindings, current)
			}
		}
		bindings = append(bindings, binding)
		if err := manifestvalidation.ValidateIdentityProfileBindings(s.runtime.Schema().Objects, bindings); err != nil {
			return req, badRequest("backend.identity.profile_binding_invalid", "diagnostic", err.Error())
		}
		return req, nil
	case "connector":
		var connector integrationmodel.ConnectorSchema
		if err := json.Unmarshal(req.Payload, &connector); err != nil {
			return req, badRequest("backend.integration.connector.definition_invalid")
		}
		return req, metadatavalidation.MetadataValidateConnectorDefinition(connector)
	case "action":
		action, err := decodeActionDefinitionPayload(req.Payload)
		if err != nil {
			return req, badRequest("backend.action.definition_invalid")
		}
		if err := firstMetadataDefinitionIssueError(validateBusinessActionDefinitionIssuesWithObjects(action, s.runtime.Schema().Objects)); err != nil {
			return req, err
		}
		action.EffectSet = nil
		normalized, _ := json.Marshal(action)
		req.Payload = normalized
		return req, nil
	case "report":
		var report reportmodel.ReportSchema
		if err := json.Unmarshal(req.Payload, &report); err != nil {
			return req, badRequest("backend.report.definition_invalid")
		}
		return req, validateReportDefinitionForSnapshot(ctx, s.runtime.Schema(), s.records, report)
	case "view":
		normalized, err := metadatavalidation.MetadataValidateViewDefinition("", req.Payload, s.runtime.Schema().Objects)
		if err != nil {
			return req, err
		}
		req.Payload = normalized
		return req, nil
	default:
		return req, nil
	}
}

func decodeActionDefinitionPayload(payload json.RawMessage) (definitionmodel.ActionSchema, error) {
	var action definitionmodel.ActionSchema
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return definitionmodel.ActionSchema{}, fmt.Errorf("action definition contains trailing JSON value")
		}
		return definitionmodel.ActionSchema{}, err
	}
	return action, nil
}

func validateBusinessActionDefinition(action definitionmodel.ActionSchema) error {
	return firstMetadataDefinitionIssueError(validateBusinessActionDefinitionIssues(action))
}
