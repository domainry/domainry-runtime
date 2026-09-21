package appschema

import (
	"bytes"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	businesscalendarpolicy "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
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
	if err := decodeClosedDefinitionPayload(payload, &value); err != nil {
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

func (s *ApplicationSchemaApplicationService) ValidateApplicationDefinition(ctx context.Context, resourceType, resourceKey string, req appschemamodel.ApplicationDefinitionUpsertRequest, principal principalmodel.Principal) (appschemamodel.ApplicationDefinitionValidationResult, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return appschemamodel.ApplicationDefinitionValidationResult{}, err
	}
	return appschemavalidation.ApplicationSchemaValidateDefinitionRequest(ctx, resourceType, resourceKey, req.Payload, principal,
		func(ctx context.Context, resourceType, resourceKey string, payload json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
			req.Payload = payload
			return s.ValidateApplicationDefinitionRequestPayload(ctx, resourceType, resourceKey, req)
		})
}

func (s *ApplicationSchemaApplicationService) ValidateApplicationDefinitionRequestPayload(ctx context.Context, resourceType, resourceKey string, req appschemamodel.ApplicationDefinitionUpsertRequest) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error) {
	if retiredPresentationDefinitionType(resourceType) {
		return nil, nil, badRequest("backend.app_schema.resource_type_unsupported", "resource_type", resourceType)
	}
	if resourceType == "object" {
		normalized, err := appschemavalidation.ApplicationSchemaValidateObjectDefinition(resourceKey, req.Payload)
		return normalized, nil, err
	}
	if resourceType == "dictionary" {
		normalized, err := appschemavalidation.ApplicationSchemaNormalizeDictionaryDefinition(resourceKey, req.Payload)
		if err != nil {
			return nil, nil, err
		}
		return normalized, nil, nil
	}
	if resourceType == "business_calendar" {
		normalizedPayload, err := materializeDefinitionRouteKey(req.Payload, strings.TrimSpace(resourceKey), "backend.business_calendar.key_mismatch")
		if err != nil {
			return nil, nil, err
		}
		var calendar businesscalendarmodel.BusinessCalendarSchema
		if err := decodeClosedDefinitionPayload(normalizedPayload, &calendar); err != nil {
			return nil, nil, badRequest("backend.business_calendar.definition_invalid")
		}
		calendar = businesscalendarpolicy.Normalize(calendar)
		if err := businesscalendarpolicy.Validate(calendar); err != nil {
			return nil, nil, badRequest(valueOrDefault(businesscalendarpolicy.ValidationCode(err), "backend.business_calendar.definition_invalid"))
		}
		payload, err := json.Marshal(calendar)
		return payload, nil, err
	}
	if resourceType == "field" {
		fieldKey := strings.TrimSpace(resourceKey)
		if separator := strings.LastIndex(fieldKey, "."); separator >= 0 {
			fieldKey = strings.TrimSpace(fieldKey[separator+1:])
		}
		normalizedPayload, err := materializeDefinitionRouteKey(req.Payload, fieldKey, "backend.metadata.field_key_mismatch")
		if err != nil {
			return nil, nil, err
		}
		req.Payload = normalizedPayload
	}
	if resourceType == "action" {
		action, err := decodeActionDefinitionPayload(req.Payload)
		if err != nil {
			return nil, []appschemamodel.ApplicationDefinitionValidationIssue{newApplicationDefinitionValidationIssue("backend.action.definition_invalid", "definition", "", "", nil)}, nil
		}
		if issues := validateBusinessActionDefinitionIssuesWithObjects(action, s.runtime.Schema().Objects); len(issues) > 0 {
			return nil, issues, nil
		}
		action.EffectSet = nil
		normalized, err := json.Marshal(action)
		return normalized, nil, err
	}
	if issues, handled := ValidateStructuredApplicationDefinition(resourceType, req.Payload); handled {
		if len(issues) > 0 {
			return nil, issues, nil
		}
		return req.Payload, nil, nil
	}
	if resourceType == "report" {
		var report reportmodel.ReportSchema
		if err := decodeClosedDefinitionPayload(req.Payload, &report); err != nil {
			return nil, nil, badRequest("backend.report.definition_invalid")
		}
		if issues := s.validateReportDefinitionIssues(ctx, report); len(issues) > 0 {
			return nil, issues, nil
		}
		return req.Payload, nil, nil
	}
	normalized, err := s.ValidateApplicationDefinitionPayload(ctx, resourceType, req)
	if err != nil {
		return nil, nil, err
	}
	return normalized.Payload, nil, nil
}

func materializeDefinitionRouteKey(payload json.RawMessage, resourceKey, mismatchCode string) (json.RawMessage, error) {
	value := map[string]any{}
	if err := json.Unmarshal(payload, &value); err != nil {
		return payload, err
	}
	resourceKey = strings.TrimSpace(resourceKey)
	if declared, exists := value["key"]; exists {
		key, ok := declared.(string)
		if !ok || strings.TrimSpace(key) != resourceKey {
			return payload, badRequest(mismatchCode, "field", resourceKey)
		}
	} else {
		value["key"] = resourceKey
	}
	normalized, err := json.Marshal(value)
	return normalized, err
}

func (s *ApplicationSchemaApplicationService) ValidateApplicationDefinitionPayload(ctx context.Context, resourceType string, req appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
	if retiredPresentationDefinitionType(resourceType) {
		return req, badRequest("backend.app_schema.resource_type_unsupported", "resource_type", resourceType)
	}
	switch resourceType {
	case "field":
		return s.normalizeAndValidateFieldMetadataMutation(ctx, req)
	case "automation_rule":
		var rule automationmodel.AutomationRuleSchema
		if err := decodeClosedDefinitionPayload(req.Payload, &rule); err != nil {
			return req, badRequest("backend.automation.definition_invalid")
		}
		return req, s.ValidateAutomationRuleDefinition(ctx, rule)
	case "identity_profile_binding":
		var binding profilebindingmodel.Binding
		if err := decodeClosedDefinitionPayload(req.Payload, &binding); err != nil {
			return req, badRequest("backend.identity.profile_binding_invalid")
		}
		if binding.ContractVersion == "" {
			binding.ContractVersion = profilebindingmodel.ContractVersion
		}
		if binding.MinReaderVersion == "" {
			binding.MinReaderVersion = profilebindingmodel.MinimumReaderVersion
		}
		if binding.Cardinality == "" {
			binding.Cardinality = "one_to_one"
		}
		req.Payload, _ = json.Marshal(binding)
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
	case "action":
		action, err := decodeActionDefinitionPayload(req.Payload)
		if err != nil {
			return req, badRequest("backend.action.definition_invalid")
		}
		if err := firstApplicationDefinitionIssueError(validateBusinessActionDefinitionIssuesWithObjects(action, s.runtime.Schema().Objects)); err != nil {
			return req, err
		}
		action.EffectSet = nil
		normalized, _ := json.Marshal(action)
		req.Payload = normalized
		return req, nil
	case "report":
		var report reportmodel.ReportSchema
		if err := decodeClosedDefinitionPayload(req.Payload, &report); err != nil {
			return req, badRequest("backend.report.definition_invalid")
		}
		return req, validateReportDefinitionForSnapshot(ctx, s.runtime.Schema(), s.records, report)
	default:
		return req, nil
	}
}

func retiredPresentationDefinitionType(resourceType string) bool {
	switch resourceType {
	case "view", "component", "entrypoint", "connector":
		return true
	default:
		return false
	}
}

func decodeActionDefinitionPayload(payload json.RawMessage) (definitionmodel.ActionSchema, error) {
	var action definitionmodel.ActionSchema
	if err := decodeClosedDefinitionPayload(payload, &action); err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	return action, nil
}

func decodeClosedDefinitionPayload(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.Contains(err.Error(), "unexpected EOF") {
			return fmt.Errorf("unexpected end of JSON input")
		}
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("definition contains trailing JSON value")
		}
		return err
	}
	return nil
}

func validateBusinessActionDefinition(action definitionmodel.ActionSchema) error {
	return firstApplicationDefinitionIssueError(validateBusinessActionDefinitionIssues(action))
}
