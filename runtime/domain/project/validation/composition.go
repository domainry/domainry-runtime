package projectvalidation

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

// ValidateComposition closes references between the JSON-owned storage and
// authorization model and the frozen code-owned behavior registry.
func ValidateComposition(model projectmodel.Model, registry *runtimeext.ProjectExtensionRegistry, modulePermissions map[string]bool) error {
	knownPermissions := map[string]bool{}
	for key, allowed := range modulePermissions {
		if allowed {
			knownPermissions[strings.TrimSpace(key)] = true
		}
	}
	for key, object := range model.Objects {
		capabilities := object.Capabilities
		if capabilities == nil {
			knownPermissions[key+".create"] = true
			knownPermissions[key+".read"] = true
			knownPermissions[key+".update"] = true
			knownPermissions[key+".delete"] = true
			knownPermissions[key+".export"] = true
			continue
		}
		for operation, enabled := range map[string]bool{"create": capabilities.Create, "read": capabilities.Read, "update": capabilities.Update, "delete": capabilities.Delete, "export": capabilities.Export} {
			if enabled {
				knownPermissions[key+"."+operation] = true
			}
		}
	}
	if registry != nil {
		for _, handler := range registry.BusinessHandlerDescriptors() {
			knownPermissions[strings.TrimSpace(handler.ActionKey)] = true
		}
		for _, report := range registry.ProjectDefinitions().Reports {
			for _, permission := range report.Report.RequiredPermissions {
				knownPermissions[strings.TrimSpace(permission)] = true
			}
		}
		for _, entrypoint := range registry.ProjectDefinitions().AgentEntrypoints {
			for _, permission := range entrypoint.RequiredPermissions {
				knownPermissions[strings.TrimSpace(permission)] = true
			}
		}
	}
	if err := projectmodel.Validate(model, nil); err != nil {
		return err
	}
	for roleKey, role := range model.Roles {
		for index, permission := range role.Permissions {
			key := strings.TrimSpace(permission.PermissionKey)
			prefix, _, hasOperation := strings.Cut(key, ".")
			if _, projectObject := model.Objects[prefix]; projectObject && hasOperation && !knownPermissions[key] {
				return &projectmodel.ValidationError{Issues: []projectmodel.Issue{{
					Code: "project_model.permission_unknown", Pointer: fmt.Sprintf("/roles/%s/permissions/%d/permission_key", roleKey, index),
					Message: fmt.Sprintf("permission %q is not published by the object or code registry", key),
				}}}
			}
		}
	}
	if registry == nil {
		return nil
	}

	issues := []projectmodel.Issue{}
	add := func(code, pointer, message string) {
		issues = append(issues, projectmodel.Issue{Code: code, Pointer: pointer, Message: message})
	}
	definitions := registry.ProjectDefinitions()
	handlers := map[string]runtimeext.HandlerDescriptor{}
	for _, handler := range registry.BusinessHandlerDescriptors() {
		handlers[handler.ActionKey] = handler
		definition, definitionErr := handler.ActionDefinition()
		if definitionErr != nil {
			add("project_definition.handler_operation_invalid", "/definitions/handlers/"+handler.ActionKey, definitionErr.Error())
		} else if _, found := model.Objects[definition.ObjectKey]; !found {
			add("project_definition.handler_target_unknown", "/definitions/handlers/"+handler.ActionKey+"/object_key", fmt.Sprintf("handler targets unknown object %q", definition.ObjectKey))
		}
		for _, capability := range handler.ObjectCapabilities {
			if _, found := model.Objects[capability.ObjectKey]; !found {
				add("project_definition.handler_object_unknown", "/definitions/handlers/"+handler.ActionKey, fmt.Sprintf("handler references unknown object %q", capability.ObjectKey))
			}
		}
	}

	runtimeObjects, err := projectmodel.RuntimeObjects(model)
	if err != nil {
		return err
	}
	objectCatalog := make(map[string]bool, len(runtimeObjects))
	legacyObjectCatalog := make(map[string]definitionmodel.ObjectSchema, len(runtimeObjects))
	for _, object := range runtimeObjects {
		objectCatalog[object.Key] = true
		legacyObjectCatalog[object.Key] = object
	}
	for _, report := range definitions.Reports {
		if _, compileErr := reportcontract.CompileReportObjectSQL(*report.Report.ObjectSQLV1, legacyObjectCatalog); compileErr != nil {
			add("project_definition.report_object_sql_invalid", "/definitions/reports/"+report.Report.Key+"/object_sql_v1", compileErr.Error())
		}
	}
	for _, public := range definitions.PublicResources {
		path := "/definitions/public_resources/" + public.Resource.Key
		object, found := legacyObjectCatalog[public.ObjectKey]
		if !found {
			add("project_definition.public_resource_object_unknown", path+"/object_key", fmt.Sprintf("public resource references unknown object %q", public.ObjectKey))
			continue
		}
		fields := objectFieldsByKey(object)
		accessField, accessFound := fields[strings.TrimSpace(public.Resource.AccessKeyField)]
		if !accessFound || strings.TrimSpace(accessField.Type) != "text" || !accessField.Unique {
			add("project_definition.public_resource_access_key_invalid", path+"/resource/access_key_field", "public resource access key must reference a unique text field")
		}
		stateField, stateFound := fields[strings.TrimSpace(public.Resource.StateField)]
		if !stateFound {
			add("project_definition.public_resource_state_field_unknown", path+"/resource/state_field", fmt.Sprintf("public resource state field %q does not exist", public.Resource.StateField))
		} else if len(stateField.Validation.Options) != 0 && !containsString(stateField.Validation.Options, public.Resource.ActiveState) {
			add("project_definition.public_resource_active_state_invalid", path+"/resource/active_state", fmt.Sprintf("active state %q is not allowed by field %q", public.Resource.ActiveState, public.Resource.StateField))
		}
		for _, fieldKey := range public.Resource.Fields {
			if _, exists := fields[strings.TrimSpace(fieldKey)]; !exists {
				add("project_definition.public_resource_field_unknown", path+"/resource/fields", fmt.Sprintf("public field %q does not exist", fieldKey))
			}
		}
		for _, file := range public.Resource.Files {
			relation, exists := fields[strings.TrimSpace(file.FieldKey)]
			target, targetExists := legacyObjectCatalog[strings.TrimSpace(relation.Validation.Target)]
			if !exists || strings.TrimSpace(relation.Validation.Target) == "" || !targetExists {
				add("project_definition.public_resource_file_relation_invalid", path+"/resource/files", fmt.Sprintf("public file field %q must reference an existing object", file.FieldKey))
				continue
			}
			targetFields := objectFieldsByKey(target)
			for name, key := range map[string]string{
				"file_id_field": file.FileIDField, "filename_field": file.FilenameField, "media_type_field": file.MediaTypeField,
				"byte_size_field": file.ByteSizeField, "content_sha256_field": file.ContentSHA256Field,
			} {
				if _, exists := targetFields[strings.TrimSpace(key)]; !exists {
					add("project_definition.public_resource_file_metadata_unknown", path+"/resource/files/"+name, fmt.Sprintf("file metadata field %q does not exist on %q", key, target.Key))
				}
			}
			for name, key := range map[string]string{"disabled_boolean_field": file.DisabledBooleanField, "disabled_timestamp_field": file.DisabledTimestampField} {
				if strings.TrimSpace(key) != "" {
					if _, exists := targetFields[strings.TrimSpace(key)]; !exists {
						add("project_definition.public_resource_file_state_unknown", path+"/resource/files/"+name, fmt.Sprintf("file state field %q does not exist on %q", key, target.Key))
					}
				}
			}
		}
	}

	workflows := map[string]bool{}
	for _, workflow := range definitions.Workflows {
		workflows[workflow.Key] = true
		if workflow.TriggerContract != nil {
			keys := append([]string{workflow.TriggerContract.ObjectKey}, workflow.TriggerContract.ObjectKeys...)
			for _, key := range keys {
				if key != "" {
					if !objectCatalog[key] {
						add("project_definition.workflow_object_unknown", "/definitions/workflows/"+workflow.Key, fmt.Sprintf("workflow references unknown object %q", key))
					}
				}
			}
		}
		if workflow.ActionContract != nil {
			validateOperationReference(workflow.ActionContract.ActionKey, workflow.ActionContract.ObjectKey, handlers, model, "/definitions/workflows/"+workflow.Key+"/action_contract", add)
		}
		if workflow.Graph != nil {
			for _, node := range workflow.Graph.Nodes {
				if node.Contract != nil && node.Contract.Action != nil {
					validateOperationReference(node.Contract.Action.ActionKey, node.Contract.Action.ObjectKey, handlers, model, "/definitions/workflows/"+workflow.Key+"/graph/nodes/"+node.ID, add)
				}
			}
		}
	}

	reports := map[string]bool{}
	for _, report := range definitions.Reports {
		reports[report.Report.Key] = true
	}
	roles := map[string]bool{}
	for key := range model.Roles {
		roles[key] = true
	}
	for _, schedule := range definitions.Schedules {
		path := "/definitions/schedules/" + schedule.Key + "/target"
		switch schedule.Target.Owner {
		case "business_action":
			validateOperationReference(schedule.Target.Operation, schedule.Target.ObjectKey, handlers, model, path, add)
			if !roles[schedule.Target.RunAsRole] {
				add("project_definition.schedule_role_unknown", path+"/run_as_role", fmt.Sprintf("schedule references unknown role %q", schedule.Target.RunAsRole))
			}
		case "workflow":
			if !workflows[schedule.Target.Operation] {
				add("project_definition.schedule_workflow_unknown", path+"/operation", fmt.Sprintf("schedule references unknown workflow %q", schedule.Target.Operation))
			}
		case "report":
			if !reports[schedule.Target.Operation] {
				add("project_definition.schedule_report_unknown", path+"/operation", fmt.Sprintf("schedule references unknown report %q", schedule.Target.Operation))
			}
		}
	}

	for _, rule := range definitions.AutomationRules {
		path := "/definitions/automation/" + rule.Key
		if _, found := model.Objects[rule.ObjectKey]; !found {
			add("project_definition.automation_object_unknown", path+"/object_key", fmt.Sprintf("automation references unknown object %q", rule.ObjectKey))
		}
		for _, instruction := range rule.Instructions {
			switch instruction.Type {
			case "invoke_business_action":
				validateOperationReference(configString(instruction.Config, "action_key"), rule.ObjectKey, handlers, model, path+"/instructions/"+instruction.Key, add)
			case "start_workflow":
				key := configString(instruction.Config, "workflow_key")
				if !workflows[key] {
					add("project_definition.automation_workflow_unknown", path+"/instructions/"+instruction.Key, fmt.Sprintf("automation references unknown workflow %q", key))
				}
			}
		}
	}

	agents := map[string]bool{}
	for _, agent := range definitions.Agents {
		agents[agent.Key] = true
		for _, objectKey := range agent.Tools {
			_ = objectKey
		}
	}
	for _, task := range definitions.AgentTasks {
		path := "/definitions/agent_tasks/" + task.Key
		if !agents[task.AgentKey] {
			add("project_definition.agent_task_agent_unknown", path+"/agent_key", fmt.Sprintf("agent task references unknown agent %q", task.AgentKey))
		}
		for _, objectKey := range task.AllowedObjects {
			if _, found := model.Objects[objectKey]; !found {
				add("project_definition.agent_task_object_unknown", path+"/allowed_objects", fmt.Sprintf("agent task references unknown object %q", objectKey))
			}
		}
		for _, actionKey := range task.AllowedActions {
			if _, found := handlers[actionKey]; !found {
				add("project_definition.agent_task_action_unknown", path+"/allowed_actions", fmt.Sprintf("agent task references unknown handler %q", actionKey))
			}
		}
	}

	for _, mapping := range definitions.IntegrationMappings {
		path := "/definitions/integration_mappings/" + mapping.Key
		switch mapping.TargetType {
		case "action":
			validateOperationReference(mapping.ActionKey, mapping.ObjectKey, handlers, model, path, add)
		case "workflow":
			if !workflows[mapping.WorkflowKey] {
				add("project_definition.integration_workflow_unknown", path+"/workflow_key", fmt.Sprintf("integration mapping references unknown workflow %q", mapping.WorkflowKey))
			}
		case "agent_task":
			if !agents[mapping.AgentID] {
				add("project_definition.integration_agent_unknown", path+"/agent_id", fmt.Sprintf("integration mapping references unknown agent %q", mapping.AgentID))
			}
		}
	}
	if len(issues) != 0 {
		return &projectmodel.ValidationError{Issues: issues}
	}
	return nil
}

func validateOperationReference(actionKey, objectKey string, handlers map[string]runtimeext.HandlerDescriptor, model projectmodel.Model, pointer string, add func(string, string, string)) {
	handler, found := handlers[strings.TrimSpace(actionKey)]
	if !found {
		add("project_definition.operation_unknown", pointer, fmt.Sprintf("definition references unknown handler %q", actionKey))
		return
	}
	if objectKey != "" {
		if _, found := model.Objects[objectKey]; !found {
			add("project_definition.operation_object_unknown", pointer, fmt.Sprintf("definition references unknown object %q", objectKey))
		}
		matched := false
		for _, capability := range handler.ObjectCapabilities {
			matched = matched || capability.ObjectKey == objectKey
		}
		if !matched {
			add("project_definition.operation_object_undeclared", pointer, fmt.Sprintf("handler %q does not declare object %q", actionKey, objectKey))
		}
	}
}

func configString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func objectFieldsByKey(object definitionmodel.ObjectSchema) map[string]definitionmodel.FieldSchema {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	return fields
}

func containsString(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
