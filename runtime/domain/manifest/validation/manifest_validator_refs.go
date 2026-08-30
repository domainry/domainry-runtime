package validation

import (
	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"
	"sort"
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

func (state *validationState) validateObjects() {
	seen := map[string]bool{}
	for index, object := range state.manifest.Objects {
		path := fmt.Sprintf("objects[%d]", index)
		key := strings.TrimSpace(object.Key)
		if key == "" {
			state.add(path+".key", "is required")
			continue
		}
		if seen[key] {
			state.add(path+".key", "duplicate object key %q", key)
		}
		seen[key] = true
		if raw, exists := object.Config["write_policy"]; exists {
			policy, ok := raw.(string)
			if !ok || (strings.TrimSpace(policy) != "direct_crud" && strings.TrimSpace(policy) != "action_only") {
				state.add(path+".config.write_policy", "must be direct_crud or action_only")
			}
		}
		state.validateObjectLifecyclePolicy(path, object)
		state.validateObjectLedgerPolicy(path, object)
		state.validateObjectExportAssurancePolicy(path, object)
		if err := recordmodel.RecordValidateLocalizedFieldContract(object); err != nil {
			state.add(path+".fields", "%s", err)
		}
		fieldSeen := map[string]bool{}
		for fieldIndex, field := range object.Fields {
			fieldPath := fmt.Sprintf("%s.fields[%d]", path, fieldIndex)
			fieldKey := strings.TrimSpace(field.Key)
			if fieldKey == "" {
				state.add(fieldPath+".key", "is required")
				continue
			}
			if fieldSeen[fieldKey] {
				state.add(fieldPath+".key", "duplicate field key %q", fieldKey)
			}
			fieldSeen[fieldKey] = true
			if field.Type == "relation" && strings.TrimSpace(field.Validation.Target) != "" {
				target := strings.TrimSpace(field.Validation.Target)
				if _, ok := state.objects[target]; !ok && !definitioncontract.IsFoundationObjectKey(target) {
					state.add(fieldPath+".validation.target", "unknown object %q", field.Validation.Target)
				}
			}
			state.validateFieldOptions(fieldPath, field)
		}
		for validationIndex, validation := range object.Validations {
			validationPath := fmt.Sprintf("%s.validations[%d]", path, validationIndex)
			validationType := strings.TrimSpace(validation.Type)
			if validation.FieldKey != "" && state.fields[key][validation.FieldKey].Key == "" {
				state.add(validationPath+".field_key", "unknown field %q", validation.FieldKey)
			}
			fieldRefs := map[string]bool{}
			for _, fieldKey := range validation.Fields {
				fieldKey = strings.TrimSpace(fieldKey)
				if state.fields[key][fieldKey].Key == "" {
					state.add(validationPath+".fields", "unknown field %q", fieldKey)
				}
				if validationType == "composite_unique" && fieldRefs[fieldKey] {
					state.add(validationPath+".fields", "backend.definition.composite_unique_field_duplicate: %s", fieldKey)
				}
				fieldRefs[fieldKey] = true
				if validationType == "composite_unique" && strings.TrimSpace(state.fields[key][fieldKey].DisabledAt) != "" {
					state.add(validationPath+".fields", "backend.definition.composite_unique_field_disabled: %s", fieldKey)
				}
			}
			if validationType == "composite_unique" && len(validation.Fields) < 2 {
				state.add(validationPath+".fields", "backend.definition.composite_unique_fields_required")
			}
			if validationType == "conditional_unique" {
				state.validateConditionalUnique(object, validation, validationPath)
			}
			if validationType == "unique_combination" || validationType == "unique_composite" {
				state.add(validationPath+".type", "backend.definition.validation_type_unsupported: %s", validationType)
			}
			if validationType == "time_overlap" || validationType == "no_time_overlap" || validationType == "time_conflict" {
				state.add(validationPath+".type", "backend.definition.validation_type_unsupported: %s", validationType)
			}
			if validationType == "temporal_exclusion" {
				state.validateTemporalExclusion(object, validation, validationPath)
			}
			if validationType == "related_aggregate_invariant" {
				state.validateRelatedAggregateInvariant(object, validation, validationPath)
			}
			if validationType == "state_machine" {
				if code := manifestStateMachineEffectContractCode(validation); code != "" {
					state.add(validationPath+".config.transitions", "%s", code)
				}
			}
		}
	}
}

func (state *validationState) validateActions() {
	roles := make(map[string]manifestmodel.RoleSchema, len(state.manifest.Roles))
	for _, role := range state.manifest.Roles {
		roles[strings.TrimSpace(role.Key)] = role
	}
	seen := map[string]bool{}
	for index, action := range state.manifest.Actions {
		path := fmt.Sprintf("actions[%d]", index)
		key := strings.TrimSpace(action.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate action key %q", key)
		}
		seen[key] = true
		if _, ok := state.objects[strings.TrimSpace(action.ObjectKey)]; !ok {
			state.add(path+".object_key", "unknown object %q", action.ObjectKey)
		}
		if strings.TrimSpace(action.RequiresPermission) == "" {
			state.add(path+".requires_permission", "is required")
		}
		if action.Authorization != nil {
			allowed := map[string]bool{}
			if len(action.Authorization.AllowedRoles) == 0 {
				state.add(path+".authorization.allowed_roles", "at least one role is required")
			}
			for roleIndex, roleKey := range action.Authorization.AllowedRoles {
				roleKey = strings.TrimSpace(roleKey)
				rolePath := fmt.Sprintf("%s.authorization.allowed_roles[%d]", path, roleIndex)
				if roleKey == "" {
					state.add(rolePath, "is required")
				} else if allowed[roleKey] {
					state.add(rolePath, "duplicate role %q", roleKey)
				} else if role, exists := roles[roleKey]; !exists {
					state.add(rolePath, "unknown role %q", roleKey)
				} else {
					state.validateActionRoleAuthorization(rolePath, action, role)
				}
				allowed[roleKey] = true
			}
		}
		outputKeys := map[string]bool{}
		for outputIndex, field := range action.OutputFields {
			fieldPath := fmt.Sprintf("%s.output_fields[%d]", path, outputIndex)
			fieldKey := strings.TrimSpace(field.Key)
			if fieldKey == "" {
				state.add(fieldPath+".key", "is required")
			} else if outputKeys[fieldKey] {
				state.add(fieldPath+".key", "duplicate output field key %q", fieldKey)
			}
			outputKeys[fieldKey] = true
			sourceObjectKey, sourceFieldKey := strings.TrimSpace(field.SourceObjectKey), strings.TrimSpace(field.SourceFieldKey)
			if (sourceObjectKey == "") != (sourceFieldKey == "") {
				state.add(fieldPath+".source_object_key", "source_object_key and source_field_key must be declared together")
				continue
			}
			if sourceObjectKey == "" {
				continue
			}
			if _, ok := state.objects[sourceObjectKey]; !ok {
				state.add(fieldPath+".source_object_key", "unknown object %q", sourceObjectKey)
			} else if state.fields[sourceObjectKey][sourceFieldKey].Key == "" {
				state.add(fieldPath+".source_field_key", "unknown field %q on object %q", sourceFieldKey, sourceObjectKey)
			}
		}
		state.validateActionAssurancePolicy(path, action)
		for _, issue := range invocationcontract.ValidateDefaults(action.PayloadFields, action.Defaults) {
			state.add(path+".defaults."+issue.Field, "%s expected=%s actual=%s", issue.Code, issue.Expected, issue.Actual)
		}
	}
}

func (state *validationState) validateActionRoleAuthorization(path string, action definitionmodel.ActionSchema, role manifestmodel.RoleSchema) {
	permission := strings.TrimSpace(action.RequiresPermission)
	if permission != "" && !containsString(role.Permissions, permission) {
		state.add(path, "role %q lacks Action permission %q", role.Key, permission)
	}
	if action.EffectSet == nil {
		return
	}
	for _, effect := range action.EffectSet.Read {
		if !roleHasDataPermission(role, effect.ObjectKey, true, false) {
			state.add(path, "role %q lacks read data permission for object %q", role.Key, effect.ObjectKey)
		}
	}
	for _, effect := range action.EffectSet.Write {
		if !roleHasDataPermission(role, effect.ObjectKey, false, true) {
			state.add(path, "role %q lacks write data permission for object %q", role.Key, effect.ObjectKey)
		}
	}
}

func roleHasDataPermission(role manifestmodel.RoleSchema, objectKey string, read, write bool) bool {
	for _, permission := range role.DataPermissions {
		if strings.TrimSpace(permission.ObjectKey) == strings.TrimSpace(objectKey) && (!read || permission.Read) && (!write || permission.Write) {
			return true
		}
	}
	return false
}

func (state *validationState) validateIntegrationConnections() {
	seen := map[string]bool{}
	for index, connection := range state.manifest.Integrations.Connections {
		path := fmt.Sprintf("integrations.connections[%d]", index)
		key := strings.TrimSpace(connection.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate Connection key %q", key)
		}
		seen[key] = true
		connectorKey := strings.TrimSpace(connection.ConnectorKey)
		connector, exists := state.connectors[connectorKey]
		if connectorKey == "" {
			state.add(path+".connector_key", "is required")
			continue
		}
		if !exists {
			state.add(path+".connector_key", "unknown connector %q", connectorKey)
			continue
		}
		providerKey := strings.TrimSpace(connection.ProviderKey)
		if providerKey == "" {
			state.add(path+".provider_key", "a concrete Provider is required for connector %q", connectorKey)
		} else if providerKey == "multi" || providerKey == "generated" || !manifestConnectorHasProvider(connector, providerKey) {
			state.add(path+".provider_key", "unknown or non-executable Provider %q for connector %q", providerKey, connectorKey)
		}
		for configKey := range connection.Config {
			trimmed := strings.TrimSpace(configKey)
			if trimmed == "provider" || trimmed == "providers" || strings.HasSuffix(trimmed, "_providers") {
				state.add(path+".config."+configKey, "Provider selection must use provider_key")
			}
		}
		if providerKey != "" && exists && manifestConnectorHasProvider(connector, providerKey) {
			if err := integrationcontract.IntegrationValidateProviderConfig(connector, providerKey, connection.Config); err != nil {
				state.add(path+".config", "%s", err)
			}
		}
		for _, secretPath := range inlineSecretConfigPaths(connection.Config, "") {
			state.add(path+".config."+secretPath, "inline Connector secrets are not allowed in Runtime metadata; store a secret reference instead")
		}
	}
	state.validateIntegrationEventMappings()
}

func (state *validationState) validateIntegrationEventMappings() {
	workflows := map[string]definitionmodel.WorkflowSchema{}
	for _, workflow := range state.manifest.Workflows {
		workflows[strings.TrimSpace(workflow.Key)] = workflow
	}
	connectionsByProvider := map[string]bool{}
	for _, connection := range state.manifest.Integrations.Connections {
		connectionsByProvider[strings.TrimSpace(connection.ProviderKey)] = true
	}
	seen := map[string]bool{}
	for index, mapping := range state.manifest.Integrations.EventMappings {
		path := fmt.Sprintf("integrations.event_mappings[%d]", index)
		eventBindings, eventContractIssues := integrationcontract.IntegrationEventBindings(mapping)
		for _, issue := range eventContractIssues {
			state.add(path+".event_fields", "%s field=%s path=%s expected=%s actual=%s", issue.Code, issue.Field, issue.Path, issue.Expected, issue.Actual)
		}
		key := strings.TrimSpace(mapping.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate Event mapping key %q", key)
		}
		seen[key] = true
		if provider := strings.TrimSpace(mapping.Provider); provider == "" || !connectionsByProvider[provider] {
			state.add(path+".provider", "must reference a Provider selected by an Integration connection")
		}
		if strings.TrimSpace(mapping.ExternalIdentity.SubjectPath) == "" {
			state.add(path+".external_identity.subject_path", "is required")
		}
		switch strings.TrimSpace(mapping.TargetType) {
		case "workflow":
			workflow := workflows[strings.TrimSpace(mapping.WorkflowKey)]
			input := manifestMappedInvocationInput(mapping.WorkflowInput, mapping.Payload)
			for _, issue := range invocationcontract.ValidateWorkflow(workflow, invocationcontract.WorkflowEntryIntegrationEvent, input, eventBindings) {
				state.add(path+".workflow_input", "%s field=%s", issue.Code, issue.Field)
			}
		case "action":
			if strings.TrimSpace(mapping.ObjectKeyPath) != "" || strings.TrimSpace(mapping.ActionKeyPath) != "" {
				state.add(path, "dynamic Action targets are unsupported; object_key and action_key must be finite static targets")
			}
			action := state.actions[strings.TrimSpace(mapping.ActionKey)]
			input := manifestMappedInvocationInput(mapping.ActionInput, mapping.Payload)
			for _, issue := range invocationcontract.ValidateAction(action, mapping.ObjectKey, input, eventBindings) {
				state.add(path+".action_input", "%s field=%s", issue.Code, issue.Field)
			}
			if strings.TrimSpace(mapping.RecordID) == "" && strings.TrimSpace(mapping.RecordIDPath) == "" {
				state.add(path+".record_id", "record_id or record_id_path is required")
			}
		case "owner_task":
			state.validateOwnerTaskActivity(path)
		default:
			state.add(path+".target_type", "must be action, workflow, or owner_task")
		}
	}
}

func manifestMappedInvocationInput(paths map[string]string, payload map[string]any) map[string]any {
	input := map[string]any{}
	for key, path := range paths {
		reference := "$event." + strings.TrimSpace(path)
		input[key] = reference
	}
	for key, value := range payload {
		if key != "connection_key" {
			input[key] = value
		}
	}
	return input
}

func (state *validationState) validateOwnerTaskActivity(path string) {
	for _, issue := range integrationcontract.IntegrationValidateOwnerTaskActivity(state.objects["activity"]) {
		state.add(path+".target_type", "%s field=%s", issue.Code, issue.Field)
	}
}

func inlineSecretConfigPaths(value map[string]any, prefix string) []string {
	paths := []string{}
	for key, raw := range value {
		current := key
		if prefix != "" {
			current = prefix + "." + key
		}
		switch nested := raw.(type) {
		case map[string]any:
			paths = append(paths, inlineSecretConfigPaths(nested, current)...)
		default:
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			secretLike := strings.Contains(lowerKey, "password") || strings.Contains(lowerKey, "token") || strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "api_key") || lowerKey == "authorization"
			if !secretLike || strings.HasSuffix(lowerKey, "_ref") {
				continue
			}
			text := strings.TrimSpace(fmt.Sprint(raw))
			if strings.HasPrefix(text, "secret:") || strings.HasPrefix(text, "env:") || strings.HasPrefix(text, "vault:") {
				continue
			}
			paths = append(paths, current)
		}
	}
	sort.Strings(paths)
	return paths
}

func manifestConnectorHasProvider(connector integrationmodel.ConnectorSchema, providerKey string) bool {
	for _, provider := range connector.Providers {
		if strings.TrimSpace(provider.Key) == providerKey {
			return true
		}
	}
	return false
}

func firstValidationValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func manifestNotificationTemplate(manifest manifestmodel.ManifestSchema, key string) (notificationmodel.NotificationTemplate, bool) {
	for _, template := range manifest.NotificationTemplates {
		if strings.TrimSpace(template.Key) == key {
			return template, true
		}
	}
	return notificationmodel.NotificationTemplate{}, false
}
