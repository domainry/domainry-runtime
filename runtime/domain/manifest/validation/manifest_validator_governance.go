package validation

import (
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

// validateGovernance rejects policy-shaped metadata that cannot actually be
// enforced by Runtime. A partially declared policy is more dangerous than no
// policy because authoring agents may otherwise treat it as effective.
func (state *validationState) validateGovernance() {
	for objectIndex, object := range state.manifest.Objects {
		for fieldIndex, field := range object.Fields {
			path := fmt.Sprintf("objects[%d].fields[%d].config", objectIndex, fieldIndex)
			if _, declared := field.Config["scope_owner"]; declared {
				state.add(path+".scope_owner", "is Runtime-owned and must not be declared by object metadata")
			}
			for _, key := range []string{"lifecycle_subject_identity", "lifecycle_subject_file"} {
				if raw, ok := field.Config[key]; ok {
					if _, valid := raw.(bool); !valid {
						state.add(path+"."+key, "must be boolean")
					}
				}
			}
			if raw, ok := field.Config["lifecycle_erase"]; ok {
				mode, valid := raw.(string)
				mode = strings.ToLower(strings.TrimSpace(mode))
				if !valid || (mode != "retain" && mode != "anonymize" && mode != "delete") {
					state.add(path+".lifecycle_erase", "must be retain, anonymize, or delete")
				}
				file, _ := field.Config["lifecycle_subject_file"].(bool)
				if file && mode == "anonymize" {
					state.add(path+".lifecycle_erase", "file lifecycle erase must be retain or delete")
				}
			}
		}
	}
	policyKeys := map[string]bool{}
	for index, policy := range state.manifest.SensitiveFieldPolicies {
		path := fmt.Sprintf("sensitive_field_policies[%d]", index)
		key := strings.TrimSpace(policy.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if policyKeys[key] {
			state.add(path+".key", "duplicate sensitive field policy key %q", key)
		}
		policyKeys[key] = true
		objectKey := strings.TrimSpace(policy.ObjectKey)
		if state.objects[objectKey].Key == "" {
			state.add(path+".object_key", "unknown object %q", policy.ObjectKey)
		}
		if len(policy.Fields) == 0 {
			state.add(path+".fields", "at least one governed field is required")
		}
		for fieldIndex, fieldKey := range policy.Fields {
			fieldKey = strings.TrimSpace(fieldKey)
			if state.fields[objectKey][fieldKey].Key == "" {
				state.add(fmt.Sprintf("%s.fields[%d]", path, fieldIndex), "unknown field %q.%s", objectKey, fieldKey)
			}
		}
		for field, value := range map[string]string{
			"sensitivity":   policy.Sensitivity,
			"read_policy":   policy.ReadPolicy,
			"write_policy":  policy.WritePolicy,
			"export_policy": policy.ExportPolicy,
			"audit_event":   policy.AuditEvent,
			"reason":        policy.Reason,
		} {
			if strings.TrimSpace(value) == "" {
				state.add(path+"."+field, "is required for complete sensitive-field governance")
			}
		}
	}

	controlKeys := map[string]bool{}
	for index, control := range state.manifest.ReportExportControls {
		path := fmt.Sprintf("report_export_controls[%d]", index)
		key := strings.TrimSpace(control.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if controlKeys[key] {
			state.add(path+".key", "duplicate report export control key %q", key)
		}
		controlKeys[key] = true
		reportKey := strings.TrimSpace(control.ReportKey)
		if reportKey == "" {
			state.add(path+".report_key", "is required")
		} else if !manifestHasReport(state, reportKey) {
			state.add(path+".report_key", "unknown report %q", reportKey)
		}
		var report reportmodel.ReportSchema
		for _, candidate := range state.manifest.Reports {
			if strings.TrimSpace(candidate.Key) == reportKey {
				report = candidate
				break
			}
		}
		queryKeys, tagKeys := map[string]bool{}, map[string]bool{}
		for _, predicate := range report.Dataset.QueryPredicates {
			queryKeys[strings.TrimSpace(predicate.Key)] = true
		}
		for _, predicate := range report.Dataset.TagPredicates {
			tagKeys[strings.TrimSpace(predicate.Key)] = true
		}
		validatePredicateAllowlist := func(field string, values []string, declared map[string]bool) {
			seen := map[string]bool{}
			for valueIndex, value := range values {
				value = strings.TrimSpace(value)
				if value == "" || seen[value] || !declared[value] {
					state.add(fmt.Sprintf("%s.%s[%d]", path, field, valueIndex), "must name one unique predicate declared by report %q", reportKey)
				}
				seen[value] = true
			}
		}
		validatePredicateAllowlist("allowed_query_keys", control.AllowedQueryKeys, queryKeys)
		validatePredicateAllowlist("allowed_tags", control.AllowedTags, tagKeys)
		if len(control.SourceObjects) == 0 {
			state.add(path+".source_objects", "at least one governed source object is required")
		}
		for sourceIndex, objectKey := range control.SourceObjects {
			if state.objects[strings.TrimSpace(objectKey)].Key == "" {
				state.add(fmt.Sprintf("%s.source_objects[%d]", path, sourceIndex), "unknown object %q", objectKey)
			}
		}
		for _, report := range state.manifest.Reports {
			if strings.TrimSpace(report.Key) != reportKey || report.ExportScope == nil || report.ExportScope.Tags == nil {
				continue
			}
			tagObjects := []string{strings.TrimSpace(report.ExportScope.Tags.Join.ObjectKey)}
			if report.ExportScope.Tags.FamilyJoin != nil {
				tagObjects = append(tagObjects, strings.TrimSpace(report.ExportScope.Tags.FamilyJoin.ObjectKey))
			}
			for _, tagObject := range tagObjects {
				found := false
				for _, source := range control.SourceObjects {
					found = found || strings.TrimSpace(source) == tagObject
				}
				if !found {
					state.add(path+".source_objects", "backend.report.export_control_invalid: tag scope source %q is required", tagObject)
				}
			}
		}
		for policyIndex, policyKey := range control.SensitiveFieldPolicyKeys {
			if !policyKeys[strings.TrimSpace(policyKey)] {
				state.add(fmt.Sprintf("%s.sensitive_field_policy_keys[%d]", path, policyIndex), "unknown sensitive field policy %q", policyKey)
			}
		}
		for field, value := range map[string]string{
			"audit_object":    control.AuditObject,
			"download_object": control.DownloadObject,
			"reason":          control.Reason,
		} {
			if strings.TrimSpace(value) == "" {
				state.add(path+"."+field, "is required for complete report-export governance")
			}
		}
		state.validateReportExportRecordMapping(path+".record_mapping", control)
		if control.MaxRows < 1 {
			state.add(path+".max_rows", "must be at least 1")
		}
	}
}

func (state *validationState) validateReportExportRecordMapping(path string, control reportmodel.ReportExportControlSchema) {
	mapping := control.RecordMapping
	required := map[string]string{
		"audit_report_key_field":      mapping.AuditReportKeyField,
		"audit_requester_field":       mapping.AuditRequesterField,
		"audit_status_field":          mapping.AuditStatusField,
		"audit_prepared_status":       mapping.AuditPreparedStatus,
		"audit_downloaded_status":     mapping.AuditDownloadedStatus,
		"audit_denied_status":         mapping.AuditDeniedStatus,
		"audit_expired_status":        mapping.AuditExpiredStatus,
		"audit_row_count_field":       mapping.AuditRowCountField,
		"audit_scope_hash_field":      mapping.AuditScopeHashField,
		"download_audit_field":        mapping.DownloadAuditField,
		"download_filename_field":     mapping.DownloadFilenameField,
		"download_content_hash_field": mapping.DownloadContentHashField,
		"download_expires_at_field":   mapping.DownloadExpiresAtField,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			state.add(path+"."+key, "is required")
		}
	}
	if len(mapping.AuditPreparedStatuses) == 0 {
		state.add(path+".audit_prepared_statuses", "at least one accepted audit status is required")
	}
	auditObject, downloadObject := strings.TrimSpace(control.AuditObject), strings.TrimSpace(control.DownloadObject)
	state.validateReportExportMappedField(path+".audit_report_key_field", auditObject, mapping.AuditReportKeyField, []string{"text", "select"}, "")
	state.validateReportExportMappedField(path+".audit_requester_field", auditObject, mapping.AuditRequesterField, []string{"user"}, "")
	state.validateReportExportMappedField(path+".audit_status_field", auditObject, mapping.AuditStatusField, []string{"select", "text"}, "")
	state.validateReportExportMappedField(path+".audit_row_count_field", auditObject, mapping.AuditRowCountField, []string{"integer", "number"}, "")
	state.validateReportExportMappedField(path+".audit_scope_hash_field", auditObject, mapping.AuditScopeHashField, []string{"text"}, "")
	state.validateReportExportMappedField(path+".download_audit_field", downloadObject, mapping.DownloadAuditField, []string{"relation"}, auditObject)
	state.validateReportExportMappedField(path+".download_filename_field", downloadObject, mapping.DownloadFilenameField, []string{"text"}, "")
	state.validateReportExportMappedField(path+".download_content_hash_field", downloadObject, mapping.DownloadContentHashField, []string{"text"}, "")
	state.validateReportExportMappedField(path+".download_expires_at_field", downloadObject, mapping.DownloadExpiresAtField, []string{"datetime"}, "")
	state.validateReportExportMappedField(path+".download_job_id_field", downloadObject, mapping.DownloadJobIDField, []string{"text"}, "")
	state.validateReportExportMappedField(path+".download_watermarked_field", downloadObject, mapping.DownloadWatermarkedField, []string{"boolean"}, "")
	state.validateReportExportMappedField(path+".download_number_field", downloadObject, mapping.DownloadNumberField, []string{"text"}, "")
	statusField := state.fields[auditObject][strings.TrimSpace(mapping.AuditStatusField)]
	allowed := map[string]bool{}
	for _, value := range fieldAllowedValues(statusField) {
		allowed[value] = true
	}
	for index, value := range mapping.AuditPreparedStatuses {
		value = strings.TrimSpace(value)
		if value == "" {
			state.add(fmt.Sprintf("%s.audit_prepared_statuses[%d]", path, index), "is required")
		} else if len(allowed) > 0 && !allowed[value] {
			state.add(fmt.Sprintf("%s.audit_prepared_statuses[%d]", path, index), "unknown status %q for %s.%s", value, auditObject, mapping.AuditStatusField)
		}
	}
	for key, raw := range map[string]string{
		"audit_prepared_status": mapping.AuditPreparedStatus, "audit_downloaded_status": mapping.AuditDownloadedStatus,
		"audit_denied_status": mapping.AuditDeniedStatus, "audit_expired_status": mapping.AuditExpiredStatus,
	} {
		if value := strings.TrimSpace(raw); value != "" && len(allowed) > 0 && !allowed[value] {
			state.add(path+"."+key, "unknown status %q for %s.%s", value, auditObject, mapping.AuditStatusField)
		}
	}
}

func (state *validationState) validateReportExportMappedField(path, objectKey, fieldKey string, allowedTypes []string, relationTarget string) {
	fieldKey = strings.TrimSpace(fieldKey)
	if fieldKey == "" {
		return
	}
	field := state.fields[objectKey][fieldKey]
	if field.Key == "" {
		state.add(path, "unknown field %q.%s", objectKey, fieldKey)
		return
	}
	validType := false
	for _, fieldType := range allowedTypes {
		validType = validType || strings.TrimSpace(field.Type) == fieldType
	}
	if !validType {
		state.add(path, "field %q.%s must have type %s", objectKey, fieldKey, strings.Join(allowedTypes, " or "))
	}
	if relationTarget != "" && strings.TrimSpace(field.Validation.Target) != relationTarget {
		state.add(path, "field %q.%s must target %q", objectKey, fieldKey, relationTarget)
	}
}

func manifestHasReport(state *validationState, key string) bool {
	for _, report := range state.manifest.Reports {
		if strings.TrimSpace(report.Key) == key {
			return true
		}
	}
	return false
}
