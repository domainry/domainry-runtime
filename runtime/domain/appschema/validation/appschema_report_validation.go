package validation

import (
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"
	"strings"

	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func ApplicationSchemaValidateReportDefinition(ctx context.Context, workspaceID string, snapshot appschemamodel.ApplicationSchemaSnapshot, records appschemacontract.ApplicationSchemaReportEvidenceReader, report reportmodel.ReportSchema) error {
	return ApplicationSchemaFirstDefinitionIssueError(ApplicationSchemaValidateReportDefinitionIssues(ctx, workspaceID, snapshot, records, report))
}

func ApplicationSchemaValidateReportDefinitionIssues(ctx context.Context, workspaceID string, snapshot appschemamodel.ApplicationSchemaSnapshot, records appschemacontract.ApplicationSchemaReportEvidenceReader, report reportmodel.ReportSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	validator := reportDefinitionValidator{workspaceID: workspaceID, snapshot: snapshot, records: records, report: report, issues: []appschemamodel.ApplicationDefinitionValidationIssue{}}
	validator.loadRuntimeReferences()
	validator.validateIdentity()
	validator.validateSourceObjects()
	validator.validatePermissionsAndAudience()
	validator.validateExecutionScope()
	validator.validateExecutionDefinition()
	validator.validateEvidenceRequirements(ctx, true)
	return validator.issues
}

// ApplicationSchemaValidateReportDefinitionContract omits live-record evidence availability,
// which belongs to an already-started Runtime instance.
func ApplicationSchemaValidateReportDefinitionContract(ctx context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot, report reportmodel.ReportSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	validator := reportDefinitionValidator{snapshot: snapshot, report: report, issues: []appschemamodel.ApplicationDefinitionValidationIssue{}, requireObjectSQLExplicitBounds: true}
	validator.loadRuntimeReferences()
	validator.validateIdentity()
	validator.validateSourceObjects()
	validator.validatePermissionsAndAudience()
	validator.validateExecutionScope()
	validator.validateExecutionDefinition()
	validator.validateEvidenceRequirements(ctx, false)
	return validator.issues
}

type reportDefinitionValidator struct {
	workspaceID string
	snapshot    appschemamodel.ApplicationSchemaSnapshot
	records     appschemacontract.ApplicationSchemaReportEvidenceReader
	report      reportmodel.ReportSchema
	objects     map[string]definitionmodel.ObjectSchema
	sourceKeys  map[string]bool
	permissions map[string]bool
	issues      []appschemamodel.ApplicationDefinitionValidationIssue
	// requireObjectSQLExplicitBounds is set on the definition-contract path
	// (model validate / Blueprint authoring) so authored object_sql must carry
	// an explicit ORDER BY and literal LIMIT before it is ever published;
	// runtime write paths keep accepting already-published definitions.
	requireObjectSQLExplicitBounds bool
}

func (v *reportDefinitionValidator) loadRuntimeReferences() {
	v.objects = make(map[string]definitionmodel.ObjectSchema, len(v.snapshot.Objects))
	v.permissions = map[string]bool{}
	for _, object := range v.snapshot.Objects {
		v.objects[object.Key] = object
		for _, action := range []string{"read", "create", "update", "delete", "import", "export"} {
			v.permissions[object.Key+"."+action] = true
		}
	}
	for _, action := range v.snapshot.Actions {
		resource, operation := definitionmodel.ActionPermissionSubject(action)
		if resource != "" && operation != "" {
			v.permissions[resource+"."+operation] = true
		}
		if permission := strings.TrimSpace(action.RequiresPermission); permission != "" {
			v.permissions[permission] = true
		}
	}
}

func (v *reportDefinitionValidator) validateIdentity() {
	if strings.TrimSpace(v.report.Key) == "" {
		v.issue("backend.report.key_required", "key", map[string]string{"field": "key"})
	}
	datasetDefined := reportmodel.ReportDatasetDefined(v.report.Dataset)
	if v.report.ObjectSQLV1 == nil && !datasetDefined {
		v.issue("backend.report.execution_definition_missing", "dataset", nil)
		return
	}
	if v.report.ObjectSQLV1 != nil && datasetDefined {
		v.issue("backend.report.execution_definition_conflict", "object_sql_v1", nil)
		return
	}
	if v.report.ObjectSQLV1 == nil && (strings.TrimSpace(v.report.Dataset.Source.ObjectKey) == "" || strings.TrimSpace(v.report.Dataset.Source.Alias) == "") {
		v.issue("backend.report.dataset_source_invalid", "dataset.source", map[string]string{"field": "dataset.source"})
	}
}

func (v *reportDefinitionValidator) validateSourceObjects() {
	v.sourceKeys = map[string]bool{}
	objectKeys := reportmodel.ReportDatasetObjectKeys(v.report.Dataset)
	objectPath := func(index int) string {
		if index == 0 {
			return "dataset.source.object_key"
		}
		return fmt.Sprintf("dataset.joins[%d].object_key", index-1)
	}
	if v.report.ObjectSQLV1 != nil {
		objectKeys = reportmodel.ReportObjectSQLObjectKeys(v.report.ObjectSQLV1)
		objectPath = func(index int) string { return fmt.Sprintf("object_sql_v1.source_objects[%d]", index) }
	}
	for index, objectKey := range objectKeys {
		objectKey = strings.TrimSpace(objectKey)
		path := objectPath(index)
		if objectKey == "" {
			v.issue("backend.report.dataset_source_invalid", path, map[string]string{"object": objectKey, "actual": objectKey})
		} else if _, exists := v.objects[objectKey]; !exists {
			v.issue("backend.report.source_object_not_found", path, map[string]string{"object": objectKey, "actual": objectKey})
		}
		v.sourceKeys[objectKey] = true
	}
}

func (v *reportDefinitionValidator) validatePermissionsAndAudience() {
	seenPermissions := map[string]bool{}
	for index, raw := range v.report.RequiredPermissions {
		permission := strings.TrimSpace(raw)
		path := fmt.Sprintf("required_permissions[%d]", index)
		if permission == "" || seenPermissions[permission] {
			v.issue("backend.report.permission_invalid", path, map[string]string{"permission": permission, "actual": permission})
		} else if !v.permissions[permission] {
			v.issue("backend.report.permission_not_found", path, map[string]string{"permission": permission, "actual": permission})
		}
		seenPermissions[permission] = true
	}
	seenRoles := map[string]bool{}
	for index, raw := range v.report.AudienceRoles {
		role := strings.TrimSpace(raw)
		if role == "" || seenRoles[role] {
			v.issue("backend.report.audience_role_invalid", fmt.Sprintf("audience_roles[%d]", index), map[string]string{"role": role, "actual": role})
		}
		seenRoles[role] = true
	}
}

func (v *reportDefinitionValidator) validateExecutionScope() {
	if v.report.ExecutionScope == nil {
		return
	}
	if v.report.ExecutionScope.Mode != reportmodel.ReportExecutionScopeCrossWorkspaceAggregateV1 {
		v.issue("backend.report.execution_scope_invalid", "execution_scope.mode", map[string]string{"actual": v.report.ExecutionScope.Mode})
		return
	}
	if v.report.ObjectSQLV1 == nil {
		v.issue("backend.report.cross_workspace_object_sql_required", "execution_scope.mode", nil)
	}
	if len(v.report.AudienceRoles) != 1 || strings.TrimSpace(v.report.AudienceRoles[0]) != "superadmin" {
		v.issue("backend.report.cross_workspace_superadmin_audience_required", "audience_roles", nil)
	}
	if len(v.report.RequiredPermissions) == 0 {
		v.issue("backend.report.cross_workspace_permission_required", "required_permissions", nil)
	}
}

func (v *reportDefinitionValidator) validateAudienceFieldPermission(path, objectKey, fieldKey, usage string) {
	// Runtime validates the referenced business schema here and enforces the
	// resolved SDK FieldPolicy when the report executes.
	_, _, _, _ = path, objectKey, fieldKey, usage
}

func (v *reportDefinitionValidator) validateEvidenceRequirements(ctx context.Context, checkAvailability bool) {
	seen := map[string]bool{}
	for index, requirement := range v.report.EvidenceRequirements {
		objectKey := strings.TrimSpace(requirement.ObjectKey)
		path := fmt.Sprintf("evidence_requirements[%d]", index)
		if objectKey == "" || seen[objectKey] {
			v.issue("backend.report.evidence_object_invalid", path+".object_key", map[string]string{"object": objectKey, "actual": objectKey})
			continue
		}
		seen[objectKey] = true
		object, exists := v.objects[objectKey]
		if !v.sourceKeys[objectKey] {
			v.issue("backend.report.evidence_source_not_declared", path+".object_key", map[string]string{"object": objectKey, "actual": objectKey})
			continue
		}
		minimum := requirement.MinimumRecords
		if minimum < 1 {
			v.issue("backend.report.evidence_minimum_invalid", path+".minimum_records", map[string]string{"minimum": "1", "actual": fmt.Sprint(minimum)})
			continue
		}
		if !exists || !v.validateEvidenceFields(path, object, requirement.RequiredNonEmptyFields) {
			continue
		}
		if checkAvailability {
			v.validateCurrentEvidence(ctx, path, object, requirement)
		}
	}
}

func (v *reportDefinitionValidator) validateEvidenceFields(path string, object definitionmodel.ObjectSchema, fields []string) bool {
	valid := true
	seen := map[string]bool{}
	for index, raw := range fields {
		fieldKey := strings.TrimSpace(raw)
		fieldPath := fmt.Sprintf("%s.required_non_empty_fields[%d]", path, index)
		if fieldKey == "" || seen[fieldKey] {
			v.issue("backend.report.evidence_field_invalid", fieldPath, map[string]string{"field_key": fieldKey})
			valid = false
		} else if !reportObjectHasField(object, fieldKey) {
			v.issue("backend.report.evidence_field_not_found", fieldPath, map[string]string{"object": object.Key, "field_key": fieldKey, "actual": fieldKey})
			valid = false
		}
		seen[fieldKey] = true
	}
	return valid
}

func (v *reportDefinitionValidator) validateCurrentEvidence(ctx context.Context, path string, object definitionmodel.ObjectSchema, requirement reportmodel.ReportEvidenceRequirement) {
	qualified := 0
	for pageNumber := 1; ; pageNumber++ {
		page, err := v.records.ListRecords(ctx, v.workspaceID, object, recordmodel.RecordListQuery{Page: pageNumber, PageSize: 200})
		if err != nil {
			v.issue("backend.report.evidence_unavailable", path, map[string]string{"object": object.Key})
			return
		}
		for _, record := range page.Items {
			if reportRecordSatisfiesEvidence(record, requirement.RequiredNonEmptyFields) {
				qualified++
				if qualified >= requirement.MinimumRecords {
					return
				}
			}
		}
		if !page.HasNext {
			break
		}
	}
	v.issue("backend.report.evidence_insufficient", path, map[string]string{
		"object": object.Key, "minimum": fmt.Sprint(requirement.MinimumRecords), "actual": fmt.Sprint(qualified), "required_fields": strings.Join(requirement.RequiredNonEmptyFields, ","),
	})
}

func (v *reportDefinitionValidator) issue(code, path string, params map[string]string) {
	if params == nil {
		params = map[string]string{}
	}
	params["field"] = path
	v.issues = append(v.issues, NewApplicationDefinitionValidationIssue(code, path, "", "", params))
}

func reportObjectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	if strings.TrimSpace(fieldKey) == "id" {
		return true
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return true
		}
	}
	return false
}

func reportRecordSatisfiesEvidence(record recordmodel.Record, requiredFields []string) bool {
	for _, fieldKey := range requiredFields {
		if recordcontract.RecordIsEmptyValue(record.Data[strings.TrimSpace(fieldKey)]) {
			return false
		}
	}
	return true
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
