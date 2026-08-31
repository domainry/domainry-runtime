package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
)

func (s *WorkflowReferenceValidator) validateWorkflowRunAsActionPermission(ctx context.Context, workflow definitionmodel.WorkflowSchema, nodeID string, action definitionmodel.ActionSchema) []workflowmodel.WorkflowValidationIssue {
	runAs := strings.TrimSpace(workflow.RunAs)
	if runAs == "" {
		return nil
	}
	_ = action
	_, roles := s.workflowIdentityReferenceCatalog(ctx)
	if len(roles) > 0 && !roles[runAs] {
		return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.run_as_role_not_found", nodeID, runAs)}
	}
	return nil
}

func workflowActionName(action definitionmodel.ActionSchema) string {
	if strings.TrimSpace(action.Label) != "" {
		return strings.TrimSpace(action.Label)
	}
	return strings.TrimSpace(action.Key)
}

func workflowAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return workflowError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func workflowAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return workflowError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func workflowAuthorizeSystemCommand(scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return workflowError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return nil
}

func badRequest(code string, params ...string) error {
	return workflowError(apperror.KindBadRequest, code, nil, params...)
}

func forbidden(code string, params ...string) error {
	return workflowError(apperror.KindForbidden, code, nil, params...)
}

func notFound(code string, params ...string) error {
	return workflowError(apperror.KindNotFound, code, nil, params...)
}

func conflict(code string, params ...string) error {
	return workflowError(apperror.KindConflict, code, nil, params...)
}

func internalError(operation string, err error) error {
	return workflowError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func workflowError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: strings.TrimSpace(code), Params: values, Err: err}
}

func serviceErrorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) && strings.TrimSpace(appErr.Code) != "" {
		return strings.TrimSpace(appErr.Code)
	}
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func cloneIntegrationSchema(value connectormodel.IntegrationSchema) connectormodel.IntegrationSchema {
	encoded, _ := json.Marshal(value)
	var cloned connectormodel.IntegrationSchema
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func actionName(action definitionmodel.ActionSchema) string {
	if strings.TrimSpace(action.Label) != "" {
		return strings.TrimSpace(action.Label)
	}
	return strings.TrimSpace(action.Key)
}

func errorParamsOf(err error) map[string]string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorParams()
	}
	return nil
}

func parseMetricTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, strings.TrimSpace(value))
	}
	return parsed, err == nil
}

func normalizeErrorCode(kind apperror.ErrorKind, code string) string {
	if strings.Contains(strings.TrimSpace(code), ".") {
		return strings.TrimSpace(code)
	}
	switch kind {
	case apperror.KindBadRequest:
		return "backend.bad_request"
	case apperror.KindForbidden:
		return "backend.forbidden"
	case apperror.KindNotFound:
		return "backend.not_found"
	case apperror.KindConflict:
		return "backend.conflict"
	default:
		return "backend.internal"
	}
}

func errorParams(values ...string) map[string]string {
	out := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			out[key] = values[index+1]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedStrings(values []string) []string { return uniqueNonEmptyStrings(values) }

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func stringValue(value any) string {
	return strings.TrimSpace(fmt.Sprint(value))
}

func (s *WorkflowReferenceValidator) validateWorkflowResolvers(ctx context.Context, workflow definitionmodel.WorkflowSchema, nodeID string, resolvers []definitionmodel.WorkflowAssigneeResolver, userIDs, roleKeys map[string]bool) []workflowmodel.WorkflowValidationIssue {
	issues := []workflowmodel.WorkflowValidationIssue{}
	for _, resolver := range resolvers {
		switch strings.TrimSpace(resolver.Type) {
		case "users":
			if s.identity != nil && len(userIDs) > 0 {
				for _, userID := range resolver.UserIDs {
					if !userIDs[userID] {
						issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_user_not_found", nodeID, userID))
					}
				}
			}
		case "role":
			if len(roleKeys) > 0 && !roleKeys[resolver.RoleKey] {
				issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_role_not_found", nodeID, resolver.RoleKey))
			}
		case "record_field":
			field, found := s.workflowObjectField(ctx, workflow, resolver.Field)
			if !found {
				issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_field_not_found", nodeID, resolver.Field))
			} else if !workflowResolverIdentityField(field) {
				issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_field_type_invalid", nodeID, resolver.Field))
			}
		case "manager", "manager_of":
			field, found := s.workflowObjectField(ctx, workflow, resolver.UserField)
			if !found {
				issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_field_not_found", nodeID, resolver.UserField))
			} else if !workflowResolverIdentityField(field) {
				issues = append(issues, workflowReferenceIssue("backend.workflow.resolver_field_type_invalid", nodeID, resolver.UserField))
			}
		}
	}
	return issues
}

func (s *WorkflowReferenceValidator) workflowObjectHasField(ctx context.Context, workflow definitionmodel.WorkflowSchema, fieldKey string) bool {
	_, exists := s.workflowObjectField(ctx, workflow, fieldKey)
	return exists
}

func (s *WorkflowReferenceValidator) workflowObjectField(ctx context.Context, workflow definitionmodel.WorkflowSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	fieldKey = strings.TrimSpace(fieldKey)
	if fieldKey == "" {
		return definitionmodel.FieldSchema{}, false
	}
	objectKeys := workflowpolicy.WorkflowTriggerObjectKeys(workflow)
	if len(objectKeys) == 0 {
		return definitionmodel.FieldSchema{}, false
	}
	var common definitionmodel.FieldSchema
	for _, objectKey := range objectKeys {
		object, exists := s.objectMap(ctx)[objectKey]
		if !exists {
			return definitionmodel.FieldSchema{}, false
		}
		found := false
		for _, field := range object.Fields {
			if field.Key == fieldKey && field.DisabledAt == "" {
				if common.Key != "" && strings.TrimSpace(common.Type) != strings.TrimSpace(field.Type) {
					return definitionmodel.FieldSchema{}, false
				}
				common, found = field, true
				break
			}
		}
		if !found {
			return definitionmodel.FieldSchema{}, false
		}
	}
	return common, common.Key != ""
}

func workflowResolverIdentityField(field definitionmodel.FieldSchema) bool {
	fieldType := strings.TrimSpace(field.Type)
	if fieldType == "user" || fieldType == "identity_user" {
		return true
	}
	if fieldType != "relation" {
		return false
	}
	target := strings.TrimSpace(field.Validation.Target)
	if target == "" {
		target = strings.TrimSpace(fmt.Sprint(field.Config["object_key"]))
	}
	if target == "" {
		target = strings.TrimSpace(fmt.Sprint(field.Config["target"]))
	}
	return target == "identity_user"
}

func (s *WorkflowReferenceValidator) validateWorkflowActionBinding(ctx context.Context, nodeID, actionKey string, input map[string]any) []workflowmodel.WorkflowValidationIssue {
	action, exists := s.workflowActions(ctx)[actionKey]
	if !exists {
		return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.action_not_registered", nodeID, actionKey)}
	}
	fields := map[string]definitionmodel.ActionPayloadField{}
	for _, field := range action.PayloadFields {
		fields[field.Key] = field
		if field.Required && field.DefaultValue == nil && action.Defaults[field.Key] == nil {
			if _, bound := input[field.Key]; !bound {
				return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.action_required_input_missing", nodeID, field.Key)}
			}
		}
	}
	issues := []workflowmodel.WorkflowValidationIssue{}
	if len(action.PayloadFields) == 0 {
		return issues
	}
	for key := range input {
		if _, declared := fields[key]; !declared {
			issues = append(issues, workflowReferenceIssue("backend.workflow.action_input_not_declared", nodeID, key))
		}
	}
	return issues
}

func (s *WorkflowReferenceValidator) workflowActions(ctx context.Context) map[string]definitionmodel.ActionSchema {
	actions := s.schema.WorkflowSchemaSnapshot(ctx, principalmodel.Principal{}).Actions
	result := make(map[string]definitionmodel.ActionSchema, len(actions))
	for _, action := range actions {
		result[action.Key] = action
	}
	return result
}

func workflowReferenceIssue(code, nodeID, reference string) workflowmodel.WorkflowValidationIssue {
	return workflowvalidation.WorkflowReferenceValidationIssue(code, nodeID, reference)
}

func workflowValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
