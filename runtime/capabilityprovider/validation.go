package capabilityprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/modulecapability"
	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

var objectAuthoringKeys = stringSet(
	"capabilities", "config", "description", "export_assurance_policy", "fields", "i18n", "key", "ledger_policy", "lifecycle_policy", "name", "provenance", "ux", "validations", "version_history",
)

var fieldAuthoringKeys = stringSet(
	"config", "default_value", "i18n", "key", "name", "options", "provenance", "required", "type", "unique", "validation", "version_history",
)

var validationAuthoringKeys = stringSet(
	"config", "field_key", "fields", "i18n", "key", "message", "object_key", "provenance", "severity", "type",
)

var actionAuthoringKeys = stringSet(
	"assurance_policy", "audit_event", "authorization", "concurrency_field", "defaults", "handler", "i18n", "key", "kind", "module_key", "name", "object_key", "optimistic_concurrency", "payload_fields", "preconditions", "risk_level",
)

var workflowAuthoringKeys = stringSet(
	"action", "action_contract", "audit_event", "condition", "condition_contract", "dead_letter_policy", "enabled", "graph", "i18n", "idempotency_keys", "input_fields", "key", "name", "provenance", "retry", "run_as", "timeout_seconds", "trigger", "trigger_contract",
)

func validateSchemaCandidate(_ context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
	if request.Kind != "schema.object" {
		return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
	}
	object, source, err := decodeObjectAuthoringFragment(request.Candidate)
	if err != nil {
		return validationDiagnostics(invalidInputDiagnostic("schema", "$.candidate.value", err)), nil
	}
	if object.Key != request.Candidate.Key {
		return validationDiagnostics(errorDiagnostic("schema", "schema.object.key_mismatch", "$.candidate.value.key", fmt.Errorf("object key %q differs from fragment key %q", object.Key, request.Candidate.Key), nil)), nil
	}

	objects, err := referencedObjects(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("schema", "schema.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	objects = append(objects, object)
	shell := object
	shell.Fields, shell.Validations = nil, nil
	shellPayload, _ := json.Marshal(shell)
	if _, err := appschemavalidation.ApplicationSchemaValidateObjectDefinition(object.Key, shellPayload); err != nil {
		return validationDiagnostics(diagnosticFromError("schema", "schema.object.invalid", "$.candidate.value", err)), nil
	}

	seenFields := map[string]bool{}
	for index, raw := range objectSlice(source["fields"]) {
		if err := rejectUnknownKeys(raw, fieldAuthoringKeys); err != nil {
			return validationDiagnostics(invalidInputDiagnostic("schema", fmt.Sprintf("$.candidate.value.fields[%d]", index), err)), nil
		}
		fieldPayload, _ := json.Marshal(raw)
		var field definitionmodel.FieldSchema
		if err := json.Unmarshal(fieldPayload, &field); err != nil {
			return validationDiagnostics(invalidInputDiagnostic("schema", fmt.Sprintf("$.candidate.value.fields[%d]", index), err)), nil
		}
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" || seenFields[fieldKey] {
			return validationDiagnostics(errorDiagnostic("schema", "schema.object.field_identity_invalid", fmt.Sprintf("$.candidate.value.fields[%d].key", index), fmt.Errorf("field key must be non-empty and unique"), nil)), nil
		}
		seenFields[fieldKey] = true
		if _, err := appschemavalidation.ApplicationSchemaNormalizeFieldMutation(
			appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: object.Key, Name: field.Name, Payload: fieldPayload},
			objects, appschemacontract.ApplicationSchemaAuthoringFieldTypes(), nil, 0,
		); err != nil {
			return validationDiagnostics(diagnosticFromError("schema", "schema.object.field_invalid", fmt.Sprintf("$.candidate.value.fields[%d]", index), err)), nil
		}
	}
	seenValidations := map[string]bool{}
	for index, raw := range objectSlice(source["validations"]) {
		if err := rejectUnknownKeys(raw, validationAuthoringKeys); err != nil {
			return validationDiagnostics(invalidInputDiagnostic("schema", fmt.Sprintf("$.candidate.value.validations[%d]", index), err)), nil
		}
		key := strings.TrimSpace(textValue(raw["key"]))
		objectKey := strings.TrimSpace(textValue(raw["object_key"]))
		if key == "" || seenValidations[key] || objectKey != "" && objectKey != object.Key {
			return validationDiagnostics(errorDiagnostic("schema", "schema.object.validation_identity_invalid", fmt.Sprintf("$.candidate.value.validations[%d]", index), fmt.Errorf("validation key must be unique and object_key must match %q", object.Key), nil)), nil
		}
		seenValidations[key] = true
	}
	return validationDiagnostics(), nil
}

func validateActionCandidate(_ context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
	if request.Kind != "action.definition" {
		return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
	}
	action, err := decodeActionAuthoringFragment(request.Candidate)
	if err != nil {
		return validationDiagnostics(invalidInputDiagnostic("records", "$.candidate.value", err)), nil
	}
	if action.Key != request.Candidate.Key {
		return validationDiagnostics(errorDiagnostic("records", "action.definition.key_mismatch", "$.candidate.value.key", fmt.Errorf("Action key %q differs from fragment key %q", action.Key, request.Candidate.Key), nil)), nil
	}
	objects, err := referencedObjects(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("records", "action.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	issues := actionvalidation.ActionValidateDefinitionIssuesWithObjects(action, objects)
	diagnostics := make([]modulecapability.Diagnostic, 0, len(issues))
	for _, issue := range issues {
		path := strings.TrimSpace(issue.FieldPath)
		if path == "" {
			path = "value"
		}
		diagnostics = append(diagnostics, errorDiagnostic("records", issue.ErrorCode, "$.candidate.value."+path, errors.New(issue.MessageKey), issue.Params))
	}
	return validationDiagnostics(diagnostics...), nil
}

func validateWorkflowCandidate(_ context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
	if request.Kind != "workflow.definition" {
		return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
	}
	workflow, err := decodeWorkflowAuthoringFragment(request.Candidate)
	if err != nil {
		return validationDiagnostics(invalidInputDiagnostic("workflows", "$.candidate.value", err)), nil
	}
	if workflow.Key != request.Candidate.Key {
		return validationDiagnostics(errorDiagnostic("workflows", "workflow.definition.key_mismatch", "$.candidate.value.key", fmt.Errorf("workflow key %q differs from fragment key %q", workflow.Key, request.Candidate.Key), nil)), nil
	}
	if workflow.Graph != nil && workflow.Graph.Version == 0 {
		workflow.Graph.Version = 2
	}
	diagnostics := []modulecapability.Diagnostic{}
	if strings.TrimSpace(workflow.Key) == "" || strings.TrimSpace(workflow.Name) == "" {
		diagnostics = append(diagnostics, errorDiagnostic("workflows", "backend.workflow.definition_identity_required", "$.candidate.value.key", errors.New("workflow key and name are required"), nil))
	}
	if workflow.TriggerContract == nil {
		diagnostics = append(diagnostics, errorDiagnostic("workflows", "backend.workflow.trigger_contract_required", "$.candidate.value.trigger_contract", errors.New("trigger contract is required"), nil))
	} else if err := validateWorkflowFragment("workflow.trigger_contract", workflow.TriggerContract); err != nil {
		diagnostics = append(diagnostics, diagnosticFromError("workflows", "workflow.trigger.invalid", "$.candidate.value.trigger_contract", err))
	}
	if workflow.ConditionContract != nil {
		if err := validateWorkflowFragment("workflow.condition_contract", workflow.ConditionContract); err != nil {
			diagnostics = append(diagnostics, diagnosticFromError("workflows", "workflow.condition.invalid", "$.candidate.value.condition_contract", err))
		}
	}
	if err := workflowpolicy.WorkflowValidateGraph(workflow.Graph); err != nil {
		diagnostics = append(diagnostics, diagnosticFromError("workflows", "workflow.graph.invalid", "$.candidate.value.graph", err))
	}
	diagnostics = append(diagnostics, validateWorkflowReferences(request, workflow)...)
	return validationDiagnostics(diagnostics...), nil
}

func validateAutomationCandidate(ctx context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
	if request.Kind != "automation.rule" {
		return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
	}
	var rule automationmodel.AutomationRuleSchema
	if err := modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &rule); err != nil {
		return validationDiagnostics(invalidInputDiagnostic("automation", "$.candidate.value", err)), nil
	}
	if rule.Key != request.Candidate.Key {
		return validationDiagnostics(errorDiagnostic("automation", "automation.rule.key_mismatch", "$.candidate.value.key", fmt.Errorf("Automation key %q differs from fragment key %q", rule.Key, request.Candidate.Key), nil)), nil
	}
	objects, err := referencedObjects(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("automation", "automation.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	actions, err := referencedActions(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("automation", "automation.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	workflows, err := referencedWorkflows(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("automation", "automation.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	validator := automationvalidation.AutomationDefinitionValidator{Catalog: automationvalidation.AutomationDefinitionCatalog{
		Objects: objects, Actions: actions, Workflows: workflows,
	}}
	if err := validator.Validate(ctx, rule); err != nil {
		return validationDiagnostics(diagnosticFromError("automation", "automation.rule.invalid", "$.candidate.value", err)), nil
	}
	return validationDiagnostics(), nil
}

func validateProfileBindingCandidate(_ context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
	if request.Kind != "principal.profile_binding" {
		return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
	}
	object, source, err := decodeObjectAuthoringFragment(request.Candidate)
	if err != nil {
		return validationDiagnostics(invalidInputDiagnostic("profilebinding", "$.candidate.value", err)), nil
	}
	if object.Key != request.Candidate.Key {
		return validationDiagnostics(errorDiagnostic("profilebinding", "profilebinding.object.key_mismatch", "$.candidate.value.key", fmt.Errorf("object key %q differs from fragment key %q", object.Key, request.Candidate.Key), nil)), nil
	}
	ux, _ := source["ux"].(map[string]any)
	if ux != nil {
		if _, declared := ux["kind"]; !declared {
			ux["kind"] = "identity_profile_extension"
		}
	}
	if strings.TrimSpace(textValue(ux["kind"])) != "identity_profile_extension" {
		return validationDiagnostics(errorDiagnostic("profilebinding", "profilebinding.object.kind_invalid", "$.candidate.value.ux.kind", fmt.Errorf("ux.kind must be identity_profile_extension"), nil)), nil
	}
	config, ok := ux["config"].(map[string]any)
	if !ok {
		return validationDiagnostics(errorDiagnostic("profilebinding", "profilebinding.object.config_invalid", "$.candidate.value.ux.config", fmt.Errorf("ux.config must be an object"), nil)), nil
	}
	var binding profilebindingmodel.Binding
	if err := mapInto(config, &binding); err != nil {
		return validationDiagnostics(errorDiagnostic("profilebinding", "backend.identity.profile_binding_invalid", "$.candidate.value.ux.config", err, nil)), nil
	}
	binding.ContractVersion = profilebindingmodel.ContractVersion
	binding.MinReaderVersion = profilebindingmodel.MinimumReaderVersion
	binding.ObjectKey = object.Key
	if binding.Cardinality == "" {
		binding.Cardinality = "one_to_one"
	}
	if binding.DefaultVisibility == "" {
		binding.DefaultVisibility = "when_readable"
	}
	objects, err := referencedObjects(request)
	if err != nil {
		return validationDiagnostics(errorDiagnostic("profilebinding", "profilebinding.referenced_context.invalid", "$.referenced_context", err, nil)), nil
	}
	objects = append(objects, object)
	if err := manifestvalidation.ValidateIdentityProfileBindings(objects, []profilebindingmodel.Binding{binding}); err != nil {
		return validationDiagnostics(errorDiagnostic("profilebinding", "backend.identity.profile_binding_invalid", "$.candidate.value.ux.config", err, map[string]string{"diagnostic": err.Error()})), nil
	}
	return validationDiagnostics(), nil
}

func decodeObjectAuthoringFragment(fragment modulecapability.AuthoringFragment) (definitionmodel.ObjectSchema, map[string]any, error) {
	source, err := decodeAuthoringObject(fragment, objectAuthoringKeys)
	if err != nil {
		return definitionmodel.ObjectSchema{}, nil, err
	}
	var object definitionmodel.ObjectSchema
	if err := mapInto(source, &object); err != nil {
		return definitionmodel.ObjectSchema{}, nil, err
	}
	return object, source, nil
}

func decodeActionAuthoringFragment(fragment modulecapability.AuthoringFragment) (definitionmodel.ActionSchema, error) {
	source, err := decodeAuthoringObject(fragment, actionAuthoringKeys)
	if err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	normalized := cloneObject(source)
	normalized["label"] = source["name"]
	delete(normalized, "module_key")
	delete(normalized, "name")
	handler, _ := normalized["handler"].(map[string]any)
	if handler != nil {
		normalized["file_operations"] = handler["file_operations"]
	}
	delete(normalized, "handler")
	var action definitionmodel.ActionSchema
	if err := mapInto(normalized, &action); err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	return action, nil
}

func decodeWorkflowAuthoringFragment(fragment modulecapability.AuthoringFragment) (definitionmodel.WorkflowSchema, error) {
	source, err := decodeAuthoringObject(fragment, workflowAuthoringKeys)
	if err != nil {
		return definitionmodel.WorkflowSchema{}, err
	}
	delete(source, "provenance")
	var workflow definitionmodel.WorkflowSchema
	if err := mapInto(source, &workflow); err != nil {
		return definitionmodel.WorkflowSchema{}, err
	}
	return workflow, nil
}

func referencedObjects(request modulecapability.ValidationRequest) ([]definitionmodel.ObjectSchema, error) {
	fragments := modulecapability.ReferencedFragments(request, "objects")
	objects := make([]definitionmodel.ObjectSchema, 0, len(fragments))
	for _, fragment := range fragments {
		object, _, err := decodeObjectAuthoringFragment(fragment)
		if err != nil {
			return nil, err
		}
		if object.Key != fragment.Key {
			return nil, fmt.Errorf("object key %q differs from fragment key %q", object.Key, fragment.Key)
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func referencedActions(request modulecapability.ValidationRequest) ([]definitionmodel.ActionSchema, error) {
	fragments := modulecapability.ReferencedFragments(request, "actions")
	actions := make([]definitionmodel.ActionSchema, 0, len(fragments))
	for _, fragment := range fragments {
		action, err := decodeActionAuthoringFragment(fragment)
		if err != nil {
			return nil, err
		}
		if action.Key != fragment.Key {
			return nil, fmt.Errorf("Action key %q differs from fragment key %q", action.Key, fragment.Key)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func referencedWorkflows(request modulecapability.ValidationRequest) ([]definitionmodel.WorkflowSchema, error) {
	fragments := modulecapability.ReferencedFragments(request, "workflows")
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(fragments))
	for _, fragment := range fragments {
		workflow, err := decodeWorkflowAuthoringFragment(fragment)
		if err != nil {
			return nil, err
		}
		if workflow.Key != fragment.Key {
			return nil, fmt.Errorf("workflow key %q differs from fragment key %q", workflow.Key, fragment.Key)
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

func validateWorkflowReferences(request modulecapability.ValidationRequest, workflow definitionmodel.WorkflowSchema) []modulecapability.Diagnostic {
	objects := fragmentKeySet(request, "objects")
	actions := fragmentKeySet(request, "actions")
	diagnostics := []modulecapability.Diagnostic{}
	missing := func(collection, key, path string) {
		diagnostics = append(diagnostics, errorDiagnostic("workflows", "workflow.reference.not_found", path, fmt.Errorf("%s %q is not present in referenced context", collection, key), map[string]string{"collection": collection, "key": key}))
	}
	if trigger := workflow.TriggerContract; trigger != nil {
		for _, key := range append([]string{trigger.ObjectKey}, trigger.ObjectKeys...) {
			key = strings.TrimSpace(key)
			if key != "" && !objects[key] {
				missing("objects", key, "$.candidate.value.trigger_contract.object_key")
			}
		}
	}
	if workflow.Graph == nil {
		return diagnostics
	}
	for index, node := range workflow.Graph.Nodes {
		if node.Contract == nil {
			continue
		}
		path := fmt.Sprintf("$.candidate.value.graph.nodes[%d].contract", index)
		if node.Contract.Action != nil {
			key := strings.TrimSpace(node.Contract.Action.ActionKey)
			if key != "" && !actions[key] {
				missing("actions", key, path+".action.action_key")
			}
			objectKey := strings.TrimSpace(node.Contract.Action.ObjectKey)
			if objectKey != "" && !objects[objectKey] {
				missing("objects", objectKey, path+".action.object_key")
			}
		}
		if node.Contract.CC != nil {
			key := strings.TrimSpace(node.Contract.CC.NotificationActionKey)
			if key != "" && !actions[key] {
				missing("actions", key, path+".cc.notification_action_key")
			}
		}
		if node.Contract.Approval != nil {
			key := strings.TrimSpace(node.Contract.Approval.ReminderActionKey)
			if key != "" && !actions[key] {
				missing("actions", key, path+".approval.reminder_action_key")
			}
		}
	}
	return diagnostics
}

func decodeAuthoringObject(fragment modulecapability.AuthoringFragment, allowed map[string]bool) (map[string]any, error) {
	var source map[string]any
	if err := modulecapability.DecodeKeyedAuthoringValue(fragment, "key", &source); err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(source, allowed); err != nil {
		return nil, err
	}
	return source, nil
}

func rejectUnknownKeys(value map[string]any, allowed map[string]bool) error {
	for key := range value {
		if !allowed[key] {
			return fmt.Errorf("unknown authoring property %q", key)
		}
	}
	return nil
}

func mapInto(source map[string]any, target any) error {
	payload, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func cloneObject(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func objectSlice(value any) []map[string]any {
	raw, _ := value.([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func textValue(value any) string {
	text, _ := value.(string)
	return text
}

func fragmentKeySet(request modulecapability.ValidationRequest, collection string) map[string]bool {
	result := map[string]bool{}
	for _, fragment := range modulecapability.ReferencedFragments(request, collection) {
		result[fragment.Key] = true
	}
	return result
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func validateWorkflowFragment(kind string, value any) error {
	fragment, err := toJSONObject(value)
	if err != nil {
		return err
	}
	return workflowpolicy.WorkflowValidateAuthoringFragment(kind, fragment)
}

func toJSONObject(value any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func validationDiagnostics(values ...modulecapability.Diagnostic) modulecapability.ValidationResult {
	if values == nil {
		values = []modulecapability.Diagnostic{}
	}
	return modulecapability.ValidationResult{Diagnostics: values}
}

func invalidInputDiagnostic(owner, path string, err error) modulecapability.Diagnostic {
	return errorDiagnostic(owner, "runtime.authoring.fragment_invalid", path, err, nil)
}

func diagnosticFromError(owner, fallback, path string, err error) modulecapability.Diagnostic {
	code, params := errorDetails(err)
	if code == "" {
		code = fallback
	}
	return errorDiagnostic(owner, code, path, err, params)
}

func errorDiagnostic(owner, code, path string, err error, params map[string]string) modulecapability.Diagnostic {
	message := strings.TrimSpace(code)
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return modulecapability.Diagnostic{Owner: owner, RuleKey: code, Severity: modulecapability.SeverityError, FieldPath: path, Message: message, Params: params}
}

func errorDetails(err error) (string, map[string]string) {
	if err == nil {
		return "", nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return strings.TrimSpace(appErr.Code), appErr.ErrorParams()
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.ErrorCode()), coded.ErrorParams()
	}
	return "", nil
}
