package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"fmt"
	"strings"
)

func AddPresentationReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, view := range snapshot.Views {
		builder.Node("view", view.Key, view.ObjectKey, view.Name, "")
		builder.Edge("view", view.Key, "object", view.ObjectKey, "presents_object", "object_key")
		addPlainFieldConfigReferences(builder, "view", view.Key, view.ObjectKey, view.Config, "config")
	}
	// Source-owned frontend references come from registered frontend capability
	// evidence. Legacy Surface/UIBlueprint payloads are intentionally ignored:
	// they describe an obsolete schema-driven renderer rather than deployed
	// source code.
	for _, entrypoint := range snapshot.EntryPoints {
		builder.Node("business_entrypoint", entrypoint.Key, "", entrypoint.Name, "")
		builder.Edge("business_entrypoint", entrypoint.Key, "workflow", referenceConfigString(entrypoint.Config, "workflow_key"), "starts_workflow", "config.workflow_key")
		builder.Edge("business_entrypoint", entrypoint.Key, "action", referenceConfigString(entrypoint.Config, "action_key"), "invokes_action", "config.action_key")
		builder.Edge("business_entrypoint", entrypoint.Key, "report", referenceConfigString(entrypoint.Config, "report_key"), "opens_report", "config.report_key")
	}
	for _, agent := range snapshot.Agents {
		builder.Node("agent", agent.Key, "", agent.Name, "")
		for index, reportKey := range referenceStringList(agent.Config["report_keys"]) {
			builder.Edge("agent", agent.Key, "report", reportKey, "reads_report", fmt.Sprintf("config.report_keys[%d]", index))
		}
	}
}

func addPlainFieldConfigReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, fromType, fromKey, objectKey string, value any, path string) {
	if objectKey == "" || value == nil {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := path + "." + key
			if isFieldConfigKey(key) {
				for index, field := range referenceStringList(child) {
					builder.Edge(fromType, fromKey, "field", objectKey+"."+field, "presents_field", fmt.Sprintf("%s[%d]", childPath, index))
				}
			}
			addPlainFieldConfigReferences(builder, fromType, fromKey, objectKey, child, childPath)
		}
	case []any:
		for index, child := range typed {
			addPlainFieldConfigReferences(builder, fromType, fromKey, objectKey, child, fmt.Sprintf("%s[%d]", path, index))
		}
	}
}

func isFieldConfigKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "field", "field_key", "fields", "columns", "group_by", "search_fields", "sort_fields":
		return true
	default:
		return false
	}
}
