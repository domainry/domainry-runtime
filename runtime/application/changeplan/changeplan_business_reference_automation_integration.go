package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
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
	for _, report := range snapshot.Reports {
		builder.Node("report", report.Key, "", report.Name, "")
		for index, objectKey := range reportmodel.ReportDatasetObjectKeys(report.Dataset) {
			builder.Edge("report", report.Key, "object", objectKey, "reads_object", fmt.Sprintf("dataset.sources[%d]", index))
		}
		aliases := reportmodel.ReportDatasetAliasObjects(report.Dataset)
		for index, join := range report.Dataset.Joins {
			path := fmt.Sprintf("dataset.joins[%d]", index)
			for equalityIndex, equality := range join.Equalities() {
				equalityPath := fmt.Sprintf("%s.field_equalities[%d]", path, equalityIndex)
				if len(join.FieldEqualities) == 0 {
					equalityPath = path
				}
				addReportFieldReference(builder, report.Key, aliases, reportmodel.ReportDatasetField{SourceAlias: join.LeftAlias, FieldKey: equality.LeftField}, "joins_on_field", equalityPath+".left_field")
				addReportFieldReference(builder, report.Key, aliases, reportmodel.ReportDatasetField{SourceAlias: join.Alias, FieldKey: equality.RightField}, "joins_on_field", equalityPath+".right_field")
			}
		}
		for index, filter := range report.Dataset.Filters {
			addReportFieldReference(builder, report.Key, aliases, filter.Field, "filters_field", fmt.Sprintf("dataset.filters[%d].field", index))
		}
		for _, group := range []struct {
			name       string
			predicates []reportmodel.ReportDatasetPredicate
		}{{"query_predicates", report.Dataset.QueryPredicates}, {"tag_predicates", report.Dataset.TagPredicates}} {
			for index, predicate := range group.predicates {
				for filterIndex, filter := range predicate.Filters {
					addReportFieldReference(builder, report.Key, aliases, filter.Field, "filters_field", fmt.Sprintf("dataset.%s[%d].filters[%d].field", group.name, index, filterIndex))
				}
			}
		}
		for index, dimension := range report.Dataset.Dimensions {
			addReportFieldReference(builder, report.Key, aliases, dimension.Field, "groups_by_field", fmt.Sprintf("dataset.dimensions[%d].field", index))
		}
		for index, measure := range report.Dataset.Measures {
			path := fmt.Sprintf("dataset.measures[%d]", index)
			if measure.Field != nil {
				addReportFieldReference(builder, report.Key, aliases, *measure.Field, "measures_field", path+".field")
			}
			if measure.StartField != nil {
				addReportFieldReference(builder, report.Key, aliases, *measure.StartField, "measures_start_field", path+".start_field")
			}
			if measure.EndField != nil {
				addReportFieldReference(builder, report.Key, aliases, *measure.EndField, "measures_end_field", path+".end_field")
			}
		}
		if report.Dataset.Privacy != nil {
			addReportFieldReference(builder, report.Key, aliases, report.Dataset.Privacy.EntityField, "privacy_entity_field", "dataset.privacy.entity_field")
		}
		for index, analysis := range report.Dataset.Analyses {
			path := fmt.Sprintf("dataset.analyses[%d]", index)
			addReportFieldReference(builder, report.Key, aliases, analysis.EntityField, "analyzes_entity_field", path+".entity_field")
			addReportFieldReference(builder, report.Key, aliases, analysis.TimeField, "analyzes_time_field", path+".time_field")
			if analysis.EventField != nil {
				addReportFieldReference(builder, report.Key, aliases, *analysis.EventField, "analyzes_event_field", path+".event_field")
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

func addReportFieldReference(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, reportKey string, aliases map[string]string, field reportmodel.ReportDatasetField, relationship, path string) {
	objectKey := strings.TrimSpace(aliases[strings.TrimSpace(field.SourceAlias)])
	fieldKey := strings.TrimSpace(field.FieldKey)
	if objectKey == "" || fieldKey == "" {
		return
	}
	builder.Edge("report", reportKey, "field", objectKey+"."+fieldKey, relationship, path)
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
