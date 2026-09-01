package changeplan

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

type ReferenceSchema struct {
	Objects         []definitionmodel.ObjectSchema
	Actions         []definitionmodel.ActionSchema
	Workflows       []definitionmodel.WorkflowSchema
	AutomationRules []automationmodel.AutomationRuleSchema
	Reports         []reportmodel.ReportSchema
	Integrations    connectormodel.IntegrationSchema
	Agents          []agentsdk.AgentSchema
	ProfileBindings []profilebindingmodel.Binding
}

func AddSchemaReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, object := range snapshot.Objects {
		builder.Node("object", object.Key, object.Key, object.Name, "")
		for _, field := range object.Fields {
			fieldKey := object.Key + "." + field.Key
			builder.Node("field", fieldKey, object.Key, field.Name, "")
			builder.Edge("field", fieldKey, "object", object.Key, "belongs_to", "fields."+field.Key)
			if target := referenceRelationTarget(field); target != "" {
				builder.Edge("field", fieldKey, "object", target, "relation_target", "validation.target")
			}
			for _, value := range businessFieldOptionValues(field) {
				stateKey := fieldKey + ":" + value
				builder.Node("state_value", stateKey, object.Key, value, "")
				builder.Edge("state_value", stateKey, "field", fieldKey, "value_of", "options")
			}
		}
	}
}

func AddIdentityProfileReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, binding := range snapshot.ProfileBindings {
		bindingKey := strings.TrimSpace(binding.ObjectKey)
		builder.Node("identity_profile_binding", bindingKey, binding.ObjectKey, binding.BusinessIdentity.Key, "")
		builder.Edge("identity_profile_binding", bindingKey, "object", binding.ObjectKey, "binds_profile_object", "object_key")
		builder.Edge("identity_profile_binding", bindingKey, "field", binding.ObjectKey+"."+binding.IdentityRelationField, "binds_identity_field", "identity_relation_field")
		for index, field := range binding.SummaryFields {
			builder.Edge("identity_profile_binding", bindingKey, "field", binding.ObjectKey+"."+field, "reads_summary_field", fmt.Sprintf("summary_fields[%d]", index))
		}
		for index, claim := range binding.BusinessIdentity.Claims {
			builder.Edge("identity_profile_binding", bindingKey, "field", binding.ObjectKey+"."+claim.FieldKey, "publishes_claim", fmt.Sprintf("business_identity.claims[%d].field_key", index))
		}
	}
}

func AddActionReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, action := range snapshot.Actions {
		builder.Node("action", action.Key, action.ObjectKey, action.Label, "")
		builder.Node("permission", action.Key, action.ObjectKey, action.Label, "")
		builder.Edge("action", action.Key, "object", action.ObjectKey, "operates_on", "object_key")
		builder.Edge("action", action.Key, "permission", action.Key, "owns_permission", "key")
	}
}

func businessFieldOptionValues(field definitionmodel.FieldSchema) []string {
	values := append([]string(nil), field.Validation.Options...)
	values = append(values, referenceStringList(field.Options)...)
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func addStateValueMapReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, fromType, fromKey, objectKey string, values map[string]any, path string) {
	for field, raw := range values {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		builder.Edge(fromType, fromKey, "field", objectKey+"."+field, "writes_field", path+"."+field)
		value, ok := raw.(string)
		if !ok || strings.HasPrefix(value, "$") || strings.TrimSpace(value) == "" {
			continue
		}
		builder.Edge(fromType, fromKey, "state_value", objectKey+"."+field+":"+strings.TrimSpace(value), "writes_state_value", path+"."+field)
	}
}

func addConnectorOperationEdge(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, fromType, fromKey string, values map[string]any, path string) {
	connector := strings.TrimSpace(fmt.Sprint(values["connector_key"]))
	operation := strings.TrimSpace(fmt.Sprint(values["operation"]))
	if connector != "" && connector != "<nil>" && operation != "" && operation != "<nil>" {
		builder.Edge(fromType, fromKey, "connector_operation", connector+"."+operation, "invokes_connector_operation", path+".operation")
	}
}

func referenceRelationTarget(field definitionmodel.FieldSchema) string {
	target := referenceConfigString(field.Config, "object_key")
	if target == "" {
		target = referenceConfigString(field.Config, "target")
	}
	if target == "" {
		target = strings.TrimSpace(field.Validation.Target)
	}
	return target
}

func referenceConfigString(config map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(config[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func referenceValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func referenceStringList(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		return append(result, typed...)
	case []any:
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
	}
	return result
}

func referenceMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	if mapped == nil {
		return map[string]any{}
	}
	return mapped
}

func referenceMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			if mapped := referenceMap(item); len(mapped) > 0 {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}
