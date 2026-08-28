package integrationcontract

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type IntegrationOwnerTaskContractIssue struct {
	Code  string
	Field string
}

// IntegrationValidateOwnerTaskActivity closes the metadata shape consumed by
// IntegrationBuildOwnerTaskProjection before any inbound event can execute.
func IntegrationValidateOwnerTaskActivity(activity definitionmodel.ObjectSchema) []IntegrationOwnerTaskContractIssue {
	issues := []IntegrationOwnerTaskContractIssue{}
	if strings.TrimSpace(activity.Key) != "activity" {
		return []IntegrationOwnerTaskContractIssue{{Code: "integration.owner_task.activity_missing"}}
	}
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range activity.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	if field := fields["subject"]; strings.TrimSpace(field.Key) == "" || (field.Type != "text" && field.Type != "long_text") {
		issues = append(issues, IntegrationOwnerTaskContractIssue{Code: "integration.owner_task.subject_invalid", Field: "subject"})
	}
	ownerFound := false
	for _, key := range []string{"owner_id", "owner", "assignee_id", "assignee"} {
		field := fields[key]
		if field.Type == "text" || field.Type == "user" || field.Type == "relation" {
			ownerFound = true
		}
	}
	if !ownerFound {
		issues = append(issues, IntegrationOwnerTaskContractIssue{Code: "integration.owner_task.owner_invalid"})
	}
	guaranteed := map[string]bool{}
	for _, key := range []string{"subject", "owner_id", "owner", "assignee_id", "assignee", "department_id", "owner_department_id", "department", "dept_id", "activity_type", "status", "due_at", "due_date", "dueDate", "notes", "content", "body", "description"} {
		guaranteed[key] = true
	}
	for _, field := range activity.Fields {
		if field.Required && field.DefaultValue == nil && !guaranteed[strings.TrimSpace(field.Key)] {
			issues = append(issues, IntegrationOwnerTaskContractIssue{Code: "integration.owner_task.required_field_unprojected", Field: field.Key})
		}
	}
	return issues
}
