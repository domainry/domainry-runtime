package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func AddAutomationReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, rule := range snapshot.AutomationRules {
		builder.Node("automation", rule.Key, rule.ObjectKey, rule.Name, "")
		if runAs := strings.TrimSpace(rule.Execution.RunAs); runAs != "" && runAs != "initiator" {
			builder.Edge("automation", rule.Key, "role", runAs, "runs_as_role", "execution.run_as")
		}
		builder.Edge("automation", rule.Key, "object", rule.ObjectKey, "observes_object", "object_key")
		for index, field := range rule.Trigger.ChangedFields {
			builder.Edge("automation", rule.Key, "field", rule.ObjectKey+"."+field, "triggered_by_field", fmt.Sprintf("trigger.changed_fields[%d]", index))
		}
		stateField := "status"
		if len(rule.Trigger.ChangedFields) == 1 {
			stateField = rule.Trigger.ChangedFields[0]
		}
		if value := strings.TrimSpace(rule.Trigger.FromState); value != "" {
			builder.Edge("automation", rule.Key, "state_value", rule.ObjectKey+"."+stateField+":"+value, "matches_from_state", "trigger.from_state")
		}
		if value := strings.TrimSpace(rule.Trigger.ToState); value != "" {
			builder.Edge("automation", rule.Key, "state_value", rule.ObjectKey+"."+stateField+":"+value, "matches_to_state", "trigger.to_state")
		}
		addAutomationConditionReferences(builder, rule.Key, rule.ObjectKey, rule.Conditions, "conditions")
		for index, instruction := range rule.Instructions {
			path := fmt.Sprintf("instructions[%d]", index)
			switch instruction.Type {
			case "invoke_business_action":
				builder.Edge("automation", rule.Key, "action", referenceConfigString(instruction.Config, "action_key"), "invokes_action", path+".config.action_key")
			case "start_workflow":
				builder.Edge("automation", rule.Key, "workflow", referenceConfigString(instruction.Config, "workflow_key"), "starts_workflow", path+".config.workflow_key")
			case "connector_call", "enqueue_outbox":
				addConnectorOperationEdge(builder, "automation", rule.Key, map[string]any{"connector_key": instruction.ConnectorKey, "operation": instruction.Operation}, path)
			}
			addExpressionFieldReferences(builder, "automation", rule.Key, rule.ObjectKey, instruction.Input, path+".input")
			addExpressionFieldReferences(builder, "automation", rule.Key, rule.ObjectKey, instruction.Config, path+".config")
		}
	}
}

func addAutomationConditionReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, ruleKey, objectKey string, group automationmodel.AutomationConditionGroup, path string) {
	for index, clause := range group.Clauses {
		clausePath := fmt.Sprintf("%s.clauses[%d]", path, index)
		addExpressionFieldReferences(builder, "automation", ruleKey, objectKey, clause.Reference, clausePath+".reference")
		match := businessFieldReferencePattern.FindStringSubmatch(clause.Reference)
		if len(match) == 2 {
			if value, ok := clause.Value.(string); ok && strings.TrimSpace(value) != "" {
				builder.Edge("automation", ruleKey, "state_value", objectKey+"."+match[1]+":"+strings.TrimSpace(value), "checks_state_value", clausePath+".value")
			}
		}
	}
	for index, child := range group.Groups {
		addAutomationConditionReferences(builder, ruleKey, objectKey, child, fmt.Sprintf("%s.groups[%d]", path, index))
	}
}

func AddReportIntegrationReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	objects := make(map[string]definitionmodel.ObjectSchema, len(snapshot.Objects))
	for _, object := range snapshot.Objects {
		objects[object.Key] = object
	}
	for _, report := range snapshot.Reports {
		builder.Node("report", report.Key, "", report.Name, "")
		if report.ObjectSQLV1 != nil {
			sources, err := reportcontract.ReportObjectSQLSourceObjects(*report.ObjectSQLV1)
			if err == nil {
				for index, objectKey := range sources {
					path := "object_sql_v1.sql"
					if len(report.ObjectSQLV1.SourceObjects) > 0 {
						path = fmt.Sprintf("object_sql_v1.source_objects[%d]", index)
					}
					builder.Edge("report", report.Key, "object", objectKey, "reads_object", path)
				}
			}
		}
		if report.ObjectSQLV1 != nil {
			if plan, err := reportcontract.CompileReportObjectSQL(*report.ObjectSQLV1, objects); err == nil {
				for sourceIndex, source := range plan.Sources {
					for fieldIndex, fieldKey := range source.Fields {
						builder.Edge("report", report.Key, "field", source.ObjectKey+"."+fieldKey, "reads_field", fmt.Sprintf("object_sql_v1.sources[%d].fields[%d]", sourceIndex, fieldIndex))
					}
				}
			}
		}
		for index, permission := range report.RequiredPermissions {
			builder.Edge("report", report.Key, "permission", permission, "requires_permission", fmt.Sprintf("required_permissions[%d]", index))
		}
	}
	for _, connector := range snapshot.Integrations.Connectors {
		builder.Node("connector", connector.Key, "", referenceValueOrDefault(connector.Name, connector.Key), connector.Source)
		for _, operation := range connector.Operations {
			operationKey := connector.Key + "." + operation.Key
			builder.Node("connector_operation", operationKey, "", referenceValueOrDefault(operation.Name, operation.Key), connector.Source)
			builder.Edge("connector_operation", operationKey, "connector", connector.Key, "belongs_to", "operations")
			if compensation := strings.TrimSpace(operation.CompensationOperation); compensation != "" {
				builder.Edge("connector_operation", operationKey, "connector_operation", connector.Key+"."+compensation, "compensates_with", "compensation_operation")
			}
		}
	}
}

func (s *ChangePlanReferenceApplicationService) addSchedulerReferences(ctx context.Context, builder *changeplanprojection.ChangePlanReferenceGraphBuilder, principal principalmodel.Principal) error {
	if s.runtime == nil {
		return nil
	}
	definitions, err := s.runtime.PublishedSchedulerDefinitions(ctx, principal)
	if err != nil {
		return err
	}
	for _, record := range definitions {
		builder.Node("scheduler", record.ID, "", strings.TrimSpace(fmt.Sprint(record.Data["name"])), "")
		targetType := strings.TrimSpace(fmt.Sprint(record.Data["target_type"]))
		targetKey := strings.TrimSpace(fmt.Sprint(record.Data["target_key"]))
		switch targetType {
		case "workflow":
			builder.Edge("scheduler", record.ID, "workflow", strings.TrimPrefix(targetKey, "scheduled:"), "schedules_target", "target_key")
		case "report_snapshot_refresh":
			builder.Edge("scheduler", record.ID, "report", targetKey, "schedules_target", "target_key")
		}
	}
	return nil
}
