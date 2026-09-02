package projection

import recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"

	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordBuildFeaturePermissions(objects []definitionmodel.ObjectSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, principal principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
	if !principal.Known {
		return recordcontract.RecordFeaturePermissionSnapshot{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.role.unknown"}
	}
	result := recordcontract.RecordFeaturePermissionSnapshot{
		RoleKey:   principal.RoleKey,
		UserID:    principal.UserID,
		Objects:   make([]recordcontract.RecordFeatureObjectPermissions, 0, len(objects)),
		Actions:   make([]recordcontract.RecordFeatureActionPermission, 0, len(actions)),
		Functions: functionPermissionSnapshots(principal),
		Data:      make([]recordcontract.RecordDataScopePermission, 0, len(objects)),
		Fields:    []recordcontract.RecordFieldPermissionSnapshot{},
		Exports:   make([]recordcontract.RecordExportPermissionSnapshot, 0, len(objects)),
		Approvals: []recordcontract.RecordApprovalPermissionSnapshot{},
		Workflows: workflowFeatureDecisions(principal, workflows),
	}
	for _, object := range objects {
		item := recordcontract.RecordFeatureObjectPermissions{ObjectKey: object.Key}
		for _, action := range []string{"read", "create", "update", "delete", "import", "export"} {
			item.Actions = append(item.Actions, objectFeatureDecision(principal, object.Key, action))
		}
		result.Objects = append(result.Objects, item)
		result.Data = append(result.Data, dataScopePermission(principal, object))
		result.Fields = append(result.Fields, fieldPermissionSnapshots(principal, object)...)
		result.Exports = append(result.Exports, exportPermissionSnapshot(principal, object))
	}
	for _, action := range actions {
		permissionKey := strings.TrimSpace(action.Key)
		objectKey, permissionAction := splitPermission(permissionKey)
		if objectKey == "" {
			objectKey = action.ObjectKey
		}
		if permissionAction == "" {
			permissionAction = actionName(action)
		}
		decision := objectFeatureDecision(principal, objectKey, permissionAction)
		decision.Key = action.Key
		decision.PermissionKey = permissionKey
		result.Actions = append(result.Actions, recordcontract.RecordFeatureActionPermission{
			Key:               action.Key,
			ObjectKey:         action.ObjectKey,
			Label:             action.Label,
			Kind:              action.Kind,
			PermissionKey:     permissionKey,
			DataScope:         decision.DataScope,
			Allowed:           decision.Allowed,
			Reason:            decision.Reason,
			AssuranceRequired: actionAssuranceRequired(action),
		})
		if operation, ok := approvalOperation(action); ok {
			result.Approvals = append(result.Approvals, recordcontract.RecordApprovalPermissionSnapshot{
				ActionKey:         action.Key,
				ObjectKey:         action.ObjectKey,
				Label:             action.Label,
				ApprovalOperation: operation,
				PermissionKey:     permissionKey,
				Decision:          decision,
			})
		}
	}
	sort.Slice(result.Actions, func(i, j int) bool { return result.Actions[i].Key < result.Actions[j].Key })
	sort.Slice(result.Approvals, func(i, j int) bool { return result.Approvals[i].ActionKey < result.Approvals[j].ActionKey })
	return result, nil
}

func actionAssuranceRequired(action definitionmodel.ActionSchema) []string {
	if action.AssurancePolicy == nil {
		return []string{}
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(action.AssurancePolicy.RequiredMethods))
	for _, method := range action.AssurancePolicy.RequiredMethods {
		method = strings.TrimSpace(method)
		if method == "" {
			continue
		}
		if _, exists := seen[method]; exists {
			continue
		}
		seen[method] = struct{}{}
		result = append(result, method)
	}
	sort.Strings(result)
	return result
}

func functionPermissionSnapshots(principal principalmodel.Principal) []recordcontract.RecordFeatureFunctionPermission {
	seen := map[string]struct{}{}
	permissionKeys := principal.PermissionKeys()
	capacity := len(permissionKeys)
	out := make([]recordcontract.RecordFeatureFunctionPermission, 0, capacity)
	appendPermission := func(permissionKey, reason string) {
		if _, ok := seen[permissionKey]; ok {
			return
		}
		seen[permissionKey] = struct{}{}
		out = append(out, recordcontract.RecordFeatureFunctionPermission{
			Key: permissionKey,
			Decision: recordcontract.RecordFeaturePermissionDecision{
				Key:           permissionKey,
				PermissionKey: permissionKey,
				Allowed:       true,
				Reason:        reason,
			},
		})
	}
	reason := "identity_policy"
	if principal.SystemScope.Valid() {
		reason = "runtime_system_capability"
	}
	for _, permissionKey := range permissionKeys {
		permissionKey = strings.TrimSpace(permissionKey)
		if permissionKey != "" {
			appendPermission(permissionKey, reason)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func objectFeatureDecision(principal principalmodel.Principal, objectKey string, action string) recordcontract.RecordFeaturePermissionDecision {
	permissionKey := strings.TrimSpace(objectKey) + "." + strings.TrimSpace(action)
	if allowed, handled := recordpolicy.RecordSDKAllowsObjectAction(principal, objectKey, action); handled {
		reason := "identity_policy_denied"
		if allowed {
			reason = "identity_policy_allowed"
		}
		return recordcontract.RecordFeaturePermissionDecision{
			Key: permissionKey, ObjectKey: strings.TrimSpace(objectKey), Action: strings.TrimSpace(action),
			PermissionKey: permissionKey, DataScope: "identity_policy", Allowed: allowed, Reason: reason,
		}
	}
	dataScope := recordpolicy.RecordDataScopeForPrincipal(principal, objectKey, action)
	decision := recordcontract.RecordFeaturePermissionDecision{
		Key:           permissionKey,
		ObjectKey:     strings.TrimSpace(objectKey),
		Action:        strings.TrimSpace(action),
		PermissionKey: permissionKey,
		DataScope:     dataScope,
		Allowed:       false,
		Reason:        "missing_permission",
	}
	if !principal.Known {
		decision.Reason = "role_unknown"
		return decision
	}
	if !recordpolicy.RecordAllowsObjectAction(principal, objectKey, action) {
		return decision
	}
	decision.Allowed = true
	decision.Reason = "runtime_system_capability"
	return decision
}

func dataScopePermission(principal principalmodel.Principal, object definitionmodel.ObjectSchema) recordcontract.RecordDataScopePermission {
	return recordcontract.RecordDataScopePermission{
		ObjectKey:  object.Key,
		OwnerField: recordpolicy.RecordOwnerFieldKey(object),
		OrgIDField: recordpolicy.RecordOwnerOrgIDFieldKey(object),
		Context: recordcontract.RecordDataScopeContext{
			OrgID:                 principal.OrgID,
			OrgScopeIDs:           append([]string(nil), principal.OrgScopeIDs...),
			ReportingScopeUserIDs: append([]string(nil), principal.ReportingScopeUserIDs...),
		},
		Read:  dataScopeDecision(principal, object, "read", false),
		Write: dataScopeDecision(principal, object, "write", true),
	}
}

func dataScopeDecision(principal principalmodel.Principal, object definitionmodel.ObjectSchema, action string, write bool) recordcontract.RecordDataScopeDecision {
	if allowed, handled := recordpolicy.RecordSDKAllowsObjectAction(principal, object.Key, action); handled {
		reason := "identity_policy_denied"
		if allowed {
			reason = "identity_policy_allowed"
		}
		return recordcontract.RecordDataScopeDecision{Action: action, Scope: "identity_policy", Allowed: allowed, Reason: reason}
	}
	scope := recordpolicy.RecordDataScopeForPrincipal(principal, object.Key, action)
	decision := recordcontract.RecordDataScopeDecision{
		Action:  action,
		Scope:   scope,
		Allowed: false,
		Reason:  "missing_data_permission",
	}
	if !principal.Known {
		decision.Reason = "role_unknown"
		return decision
	}
	if principal.SystemScope.Valid() && principal.Allows(object.Key, action) {
		decision.Allowed = true
		decision.Reason = "runtime_system_capability"
	}
	return decision
}

func fieldPermissionSnapshots(principal principalmodel.Principal, object definitionmodel.ObjectSchema) []recordcontract.RecordFieldPermissionSnapshot {
	out := make([]recordcontract.RecordFieldPermissionSnapshot, 0, len(object.Fields))
	for _, field := range object.Fields {
		if field.DisabledAt != "" {
			continue
		}
		out = append(out, fieldPermissionSnapshot(principal, object, field))
	}
	return out
}

func fieldPermissionSnapshot(principal principalmodel.Principal, object definitionmodel.ObjectSchema, field definitionmodel.FieldSchema) recordcontract.RecordFieldPermissionSnapshot {
	read, readMasked, readHandled := recordpolicy.RecordSDKReadableField(principal, object.Key, field.Key)
	write, _, writeHandled := recordpolicy.RecordSDKWritableField(principal, object.Key, field.Key)
	export, exportMasked, exportHandled := recordpolicy.RecordSDKExportableField(principal, object.Key, field.Key)
	if readHandled || writeHandled || exportHandled {
		return recordcontract.RecordFieldPermissionSnapshot{
			ObjectKey: object.Key, FieldKey: field.Key, FieldType: field.Type,
			Read: fieldAccessDecision(read, "identity_policy_denied"), Write: fieldAccessDecision(write, "identity_policy_denied"),
			Export: fieldAccessDecision(export, "identity_policy_denied"), Masked: readMasked || exportMasked, Source: "identity_policy",
		}
	}
	source := "identity_access_bundle_required"
	if principal.SystemScope.Valid() {
		source = "runtime_system_capability"
	}
	return recordcontract.RecordFieldPermissionSnapshot{
		ObjectKey: object.Key,
		FieldKey:  field.Key,
		FieldType: field.Type,
		Read:      fieldAccessDecision(recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field), "identity_access_bundle_required"),
		Write:     fieldAccessDecision(recordpolicy.RecordCanWriteObjectFieldForPrincipal(principal, object, field), "identity_access_bundle_required"),
		Export:    fieldAccessDecision(recordpolicy.RecordCanExportObjectFieldForPrincipal(principal, object, field), "identity_access_bundle_required"),
		Masked:    recordpolicy.RecordFieldReadMaskedForPrincipal(principal, object.Key, field.Key),
		Source:    source,
	}
}

func fieldAccessDecision(allowed bool, reason string) recordcontract.RecordFieldAccessDecision {
	if allowed {
		return recordcontract.RecordFieldAccessDecision{Allowed: true, Reason: "allowed"}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "field_permission_denied"
	}
	return recordcontract.RecordFieldAccessDecision{Allowed: false, Reason: reason}
}

func exportPermissionSnapshot(principal principalmodel.Principal, object definitionmodel.ObjectSchema) recordcontract.RecordExportPermissionSnapshot {
	decision := objectFeatureDecision(principal, object.Key, "export")
	fields := make([]recordcontract.RecordExportFieldPermission, 0, len(object.Fields))
	for _, field := range object.Fields {
		if field.DisabledAt != "" {
			continue
		}
		allowed, masked, handled := recordpolicy.RecordSDKExportableField(principal, object.Key, field.Key)
		if handled {
			fields = append(fields, recordcontract.RecordExportFieldPermission{
				FieldKey: field.Key, FieldType: field.Type,
				Export: fieldAccessDecision(allowed, "identity_policy_denied"), Masked: masked,
			})
			continue
		}
		fields = append(fields, recordcontract.RecordExportFieldPermission{
			FieldKey:  field.Key,
			FieldType: field.Type,
			Export:    fieldAccessDecision(recordpolicy.RecordCanExportObjectFieldForPrincipal(principal, object, field), "identity_access_bundle_required"),
			Masked:    recordpolicy.RecordFieldExportMaskedForPrincipal(principal, object.Key, field.Key),
		})
	}
	return recordcontract.RecordExportPermissionSnapshot{
		ObjectKey: object.Key,
		Allowed:   decision.Allowed,
		Reason:    decision.Reason,
		DataScope: decision.DataScope,
		Fields:    fields,
	}
}

func approvalOperation(action definitionmodel.ActionSchema) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(action.Key, "-", "_")))
	actionPart := strings.ToLower(strings.TrimSpace(actionName(action)))
	for _, candidate := range []string{actionPart, normalized, strings.ToLower(action.Kind)} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		switch {
		case candidate == "approve", candidate == "reject", candidate == "submit_for_approval", candidate == "request_approval":
			return candidate, true
		case strings.Contains(candidate, "approve"):
			return "approve", true
		case strings.Contains(candidate, "reject"):
			return "reject", true
		case strings.Contains(candidate, "approval"):
			return candidate, true
		}
	}
	return "", false
}

func permissionProjectionStringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func workflowFeatureDecisions(principal principalmodel.Principal, workflows []definitionmodel.WorkflowSchema) []recordcontract.RecordFeaturePermissionDecision {
	result := make([]recordcontract.RecordFeaturePermissionDecision, 0, len(workflows))
	for _, workflow := range workflows {
		if !workflow.Enabled {
			continue
		}
		actionKey := workflowcontract.RunActionKey(workflow.Key)
		if actionKey == "" {
			continue
		}
		decision := recordcontract.RecordFeaturePermissionDecision{
			Key:           actionKey,
			ObjectKey:     "workflow",
			Action:        "run",
			PermissionKey: actionKey,
			Reason:        "missing_permission",
		}
		if principal.HasExactPermission(actionKey) {
			decision.Allowed = true
			decision.Reason = "allowed"
		}
		result = append(result, decision)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func splitPermission(value string) (string, string) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return "", strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[len(parts)-2]), strings.TrimSpace(parts[len(parts)-1])
}

func actionName(action definitionmodel.ActionSchema) string {
	_, name := splitPermission(action.Key)
	return name
}
