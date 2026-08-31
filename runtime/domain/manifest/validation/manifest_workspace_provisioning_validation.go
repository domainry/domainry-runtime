package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateWorkspaceProvisioning() {
	seen := map[string]bool{}
	allowedExpressions := map[string]bool{
		"$provision.canonical_code": true, "$provision.tenant_name": true,
		"$provision.workspace_id": true, "$provision.admin_login_id": true,
		"$provision.configuration_json": true,
	}
	for index, projection := range state.manifest.WorkspaceProvisioning {
		path := fmt.Sprintf("workspace_provisioning[%d]", index)
		key := strings.TrimSpace(projection.Key)
		if key == "" || seen[key] {
			state.add(path+".key", "is required and must be unique")
		}
		seen[key] = true
		object, exists := state.objects[strings.TrimSpace(projection.ObjectKey)]
		if !exists {
			state.add(path+".object_key", "references unknown object %q", projection.ObjectKey)
			continue
		}
		if projection.Scope != "headquarters" && projection.Scope != "provisioned_workspace" {
			state.add(path+".scope", "must be headquarters or provisioned_workspace")
		}
		fields := map[string]definitionmodel.FieldSchema{}
		for _, field := range object.Fields {
			fields[field.Key] = field
		}
		for fieldKey, value := range projection.Data {
			field, ok := fields[fieldKey]
			if !ok {
				state.add(path+".data."+fieldKey, "references unknown field")
				continue
			}
			if field.Type == "relation" || field.Type == "user" {
				state.add(path+".data."+fieldKey, "relation and user fields are not supported")
			}
			if expression, ok := value.(string); ok && strings.HasPrefix(expression, "$provision.") && !allowedExpressions[expression] && !strings.HasPrefix(expression, "$provision.configuration.") {
				state.add(path+".data."+fieldKey, "uses unsupported provisioning expression %q", expression)
			}
		}
		for _, field := range object.Fields {
			if field.Required && projection.Data[field.Key] == nil && field.Default == nil && field.DefaultValue == nil {
				state.add(path+".data", "does not bind required field %q", field.Key)
			}
		}
	}
}
