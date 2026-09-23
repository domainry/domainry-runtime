package projectmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

var stableKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var runtimeOwnedObjectKeys = map[string]bool{
	"record_timer":       true,
	"record_timer_event": true,
}

type Issue struct {
	Code    string `json:"code"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Issues))
	for index, issue := range e.Issues {
		parts[index] = fmt.Sprintf("%s at %s: %s", issue.Code, issue.Pointer, issue.Message)
	}
	return strings.Join(parts, "; ")
}

// Validate checks the JSON-owned project model. knownPermissions is the
// frozen Runtime/project definition catalog; nil defers that cross-registry
// check while still validating the model's internal graph.
func Validate(model Model, knownPermissions map[string]bool) error {
	issues := []Issue{}
	add := func(code, pointer, message string) {
		issues = append(issues, Issue{Code: code, Pointer: pointer, Message: message})
	}
	if !stableKeyPattern.MatchString(strings.TrimSpace(model.Project.Key)) {
		add("project_model.project_key_invalid", "/project/key", "project key must be a stable lowercase identifier")
	}
	if strings.TrimSpace(model.Project.Name) == "" {
		add("project_model.project_name_required", "/project/name", "project name is required")
	}
	zone := strings.TrimSpace(model.Project.TimeZone)
	if zone == "" {
		zone = "UTC"
	}
	if zone == "Local" {
		add("project_model.time_zone_invalid", "/project/time_zone", "time zone must be UTC or a canonical IANA name")
	} else if _, err := time.LoadLocation(zone); err != nil {
		add("project_model.time_zone_invalid", "/project/time_zone", err.Error())
	}
	if len(model.Objects) == 0 {
		add("project_model.objects_required", "/objects", "at least one business object is required")
	}
	for key, object := range model.Objects {
		path := "/objects/" + pointerToken(key)
		if !stableKeyPattern.MatchString(key) || object.Key != key {
			add("project_model.object_key_invalid", path, "object map key must be a stable lowercase identifier")
		}
		if runtimeOwnedObjectKeys[key] {
			add("project_model.object_key_reserved", path, "object key is owned by Runtime infrastructure")
		}
		if strings.TrimSpace(object.Name) == "" {
			add("project_model.object_name_required", path+"/name", "object name is required")
		}
		validateObjectFields(model, object, path, add)
	}
	for key, role := range model.Roles {
		path := "/roles/" + pointerToken(key)
		if !stableKeyPattern.MatchString(key) || role.Key != key {
			add("project_model.role_key_invalid", path, "role map key must be a stable lowercase identifier")
		}
		if strings.TrimSpace(role.Name) == "" {
			add("project_model.role_name_required", path+"/name", "role name is required")
		}
		seenPermissions := map[string]bool{}
		for index, permission := range role.Permissions {
			permissionKey := strings.TrimSpace(permission.PermissionKey)
			permissionPath := fmt.Sprintf("%s/permissions/%d/permission_key", path, index)
			if permissionKey == "" || seenPermissions[permissionKey] {
				add("project_model.permission_invalid", permissionPath, "permission key must be non-empty and unique within the role")
			}
			seenPermissions[permissionKey] = true
			if knownPermissions != nil && !knownPermissions[permissionKey] {
				add("project_model.permission_unknown", permissionPath, fmt.Sprintf("permission %q is not published by an object or code registry", permissionKey))
			}
			if permission.DataPolicy == nil && !permission.DataScope.Valid() {
				add("project_model.data_scope_invalid", fmt.Sprintf("%s/permissions/%d/data_scope", path, index), "permission requires one supported data scope")
			}
			if permission.DataPolicy != nil && permission.DataScope != "" {
				add("project_model.data_policy_ambiguous", fmt.Sprintf("%s/permissions/%d", path, index), "data_scope and data_policy are mutually exclusive")
			}
		}
		validateRoleObjectPolicies(model, role, path, add)
	}
	adminRole := strings.TrimSpace(model.Project.InitialWorkspaceAdministratorRole)
	if adminRole == "" || model.Roles[adminRole].Key == "" {
		add("project_model.initial_admin_role_invalid", "/project/initial_workspace_administrator_role", "initial administrator role must reference a declared role")
	}
	validateIdentityProfiles(model, add)
	if len(issues) == 0 {
		return nil
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Pointer == issues[j].Pointer {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].Pointer < issues[j].Pointer
	})
	return &ValidationError{Issues: issues}
}

func validateObjectFields(model Model, object Object, path string, add func(string, string, string)) {
	if len(object.Fields) == 0 {
		add("project_model.object_fields_required", path+"/fields", "object requires at least one field")
	}
	seen := map[string]bool{}
	for mapKey, field := range object.Fields {
		fieldPath := path + "/fields/" + pointerToken(mapKey)
		key := strings.TrimSpace(field.Key)
		if !stableKeyPattern.MatchString(key) || key != mapKey || seen[key] {
			add("project_model.field_key_invalid", fieldPath+"/key", "field key must be a unique stable lowercase identifier")
		}
		seen[key] = true
		if strings.TrimSpace(field.Name) == "" {
			add("project_model.field_name_required", fieldPath+"/name", "field name is required")
		}
		if len(field.Default) != 0 && !json.Valid(field.Default) {
			add("project_model.field_default_invalid", fieldPath+"/default", "field default must be valid JSON")
		}
		if strings.TrimSpace(field.Type) == "relation" {
			target := ""
			if field.Relation != nil {
				target = strings.TrimSpace(field.Relation.TargetObjectKey)
			}
			if target == "" {
				add("project_model.relation_target_required", fieldPath, "relation field requires a target object")
			} else if _, exists := model.Objects[target]; !exists && target != "identity_user" && target != "organization_unit" {
				add("project_model.relation_target_unknown", fieldPath, fmt.Sprintf("relation target %q is not declared", target))
			}
		} else if field.Relation != nil {
			add("project_model.relation_target_unexpected", fieldPath+"/relation", "only relation fields may declare relation metadata")
		}
	}
	for key, constraint := range object.UniqueConstraints {
		constraintPath := path + "/unique_constraints/" + pointerToken(key)
		if !stableKeyPattern.MatchString(key) || constraint.Key != key {
			add("project_model.unique_constraint_key_invalid", constraintPath, "unique constraint key must be a stable lowercase identifier")
		}
		if len(constraint.Fields) < 2 {
			add("project_model.unique_constraint_fields_invalid", constraintPath+"/fields", "composite unique constraint requires at least two fields")
		}
		constraintFields := map[string]bool{}
		for _, fieldKey := range constraint.Fields {
			if _, exists := object.Fields[fieldKey]; !exists || constraintFields[fieldKey] {
				add("project_model.unique_constraint_field_invalid", constraintPath+"/fields", fmt.Sprintf("field %q must exist and occur once", fieldKey))
			}
			constraintFields[fieldKey] = true
		}
	}
}

func validateRoleObjectPolicies(model Model, role Role, path string, add func(string, string, string)) {
	fieldExists := func(objectKey, fieldKey string) bool {
		object, exists := model.Objects[objectKey]
		if !exists {
			return false
		}
		if _, found := object.Fields[fieldKey]; found {
			return true
		}
		return fieldKey == "id"
	}
	for index, policy := range role.FieldPermissions {
		if !fieldExists(policy.ObjectKey, policy.FieldKey) {
			add("project_model.field_permission_target_unknown", fmt.Sprintf("%s/field_permissions/%d", path, index), "field permission references an unknown object or field")
		}
	}
	for index, policy := range role.ReferencePermissions {
		object, exists := model.Objects[policy.SourceObjectKey]
		if !exists || !fieldExists(policy.SourceObjectKey, policy.RelationFieldKey) || model.Objects[policy.TargetObjectKey].Key == "" || !relationTargets(object, policy.RelationFieldKey, policy.TargetObjectKey) {
			add("project_model.reference_permission_target_invalid", fmt.Sprintf("%s/reference_permissions/%d", path, index), "reference permission must match a declared relation")
		}
		for _, field := range policy.DisplayFields {
			if !fieldExists(policy.TargetObjectKey, field) {
				add("project_model.reference_display_field_unknown", fmt.Sprintf("%s/reference_permissions/%d/display_fields", path, index), fmt.Sprintf("target field %q is not declared", field))
			}
		}
	}
	for index, rule := range role.ExportRules {
		if _, exists := model.Objects[rule.ObjectKey]; !exists {
			add("project_model.export_rule_object_unknown", fmt.Sprintf("%s/export_rules/%d/object_key", path, index), "export rule references an unknown object")
		}
		for _, field := range rule.Fields {
			if !fieldExists(rule.ObjectKey, field) {
				add("project_model.export_rule_field_unknown", fmt.Sprintf("%s/export_rules/%d/fields", path, index), fmt.Sprintf("export field %q is not declared", field))
			}
		}
	}
}

func validateIdentityProfiles(model Model, add func(string, string, string)) {
	for key, binding := range model.IdentityProfiles {
		path := "/identity_profiles/" + pointerToken(key)
		object, exists := model.Objects[binding.ObjectKey]
		if !stableKeyPattern.MatchString(key) || binding.BusinessIdentity.Key != key {
			add("project_model.identity_profile_key_invalid", path, "identity profile map key must be a stable lowercase identifier")
		}
		if !exists {
			add("project_model.identity_profile_object_unknown", path+"/object_key", "identity profile references an unknown object")
			continue
		}
		if binding.Cardinality == "" {
			binding.Cardinality = "one_to_one"
		}
		if binding.Cardinality != "one_to_one" {
			add("project_model.identity_profile_cardinality_invalid", path+"/cardinality", "only one_to_one is supported")
		}
		found := false
		if field, fieldFound := object.Fields[binding.IdentityRelationField]; fieldFound {
			found = field.Type == "relation" && field.Unique && relationFieldTarget(field) == "identity_user"
		}
		if !found {
			add("project_model.identity_profile_relation_invalid", path+"/identity_relation_field", "identity relation must be a unique relation to identity_user")
		}
		switch binding.DefaultVisibility {
		case "when_readable", "hidden":
		default:
			add("project_model.identity_profile_visibility_invalid", path+"/default_visibility", "visibility must be when_readable or hidden")
		}
	}
}

func relationTargets(object Object, fieldKey, target string) bool {
	if field, found := object.Fields[fieldKey]; found {
		return field.Type == "relation" && relationFieldTarget(field) == target
	}
	return false
}

func relationFieldTarget(field Field) string {
	if field.Relation != nil {
		return strings.TrimSpace(field.Relation.TargetObjectKey)
	}
	return ""
}

// RuntimeObjects converts the closed project contract into Runtime's current
// internal storage definition type. The conversion is one-way and never
// exposes legacy generic Config fields in model.json.
func RuntimeObjects(model Model) ([]definitionmodel.ObjectSchema, error) {
	objectKeys := make([]string, 0, len(model.Objects))
	for key := range model.Objects {
		objectKeys = append(objectKeys, key)
	}
	sort.Strings(objectKeys)
	result := make([]definitionmodel.ObjectSchema, 0, len(objectKeys))
	for _, objectKey := range objectKeys {
		object := model.Objects[objectKey]
		fieldKeys := make([]string, 0, len(object.Fields))
		for key := range object.Fields {
			fieldKeys = append(fieldKeys, key)
		}
		sort.Strings(fieldKeys)
		fields := make([]definitionmodel.FieldSchema, 0, len(fieldKeys))
		for _, fieldKey := range fieldKeys {
			field := object.Fields[fieldKey]
			var defaultValue any
			if len(field.Default) != 0 {
				if err := json.Unmarshal(field.Default, &defaultValue); err != nil {
					return nil, fmt.Errorf("decode default for %s.%s: %w", objectKey, fieldKey, err)
				}
			}
			fields = append(fields, definitionmodel.FieldSchema{
				Key: fieldKey, Name: field.Name, Description: field.Description, Type: field.Type, I18n: field.I18n,
				Validation: definitionmodel.FieldValidation{MinLength: field.Validation.MinLength, MaxLength: field.Validation.MaxLength, Min: field.Validation.Min, Max: field.Validation.Max, Pattern: field.Validation.Pattern, Options: append([]string(nil), field.Validation.Options...), Target: relationFieldTarget(field)},
				Required:   field.Required, Unique: field.Unique, Default: defaultValue, Sensitive: field.Sensitive,
			})
		}
		constraintKeys := make([]string, 0, len(object.UniqueConstraints))
		for key := range object.UniqueConstraints {
			constraintKeys = append(constraintKeys, key)
		}
		sort.Strings(constraintKeys)
		validations := make([]definitionmodel.ValidationSchema, 0, len(constraintKeys))
		for _, key := range constraintKeys {
			constraint := object.UniqueConstraints[key]
			validations = append(validations, definitionmodel.ValidationSchema{Key: key, ObjectKey: objectKey, Type: "composite_unique", Fields: append([]string(nil), constraint.Fields...)})
		}
		result = append(result, definitionmodel.ObjectSchema{
			Key: objectKey, Name: object.Name, Description: object.Description, I18n: object.I18n, Fields: fields, Validations: validations,
			Capabilities: object.Capabilities, LifecyclePolicy: object.LifecyclePolicy, LedgerPolicy: object.LedgerPolicy,
			ExportAssurancePolicy: object.ExportAssurancePolicy,
		})
	}
	return result, nil
}

func ContentHash(model Model) (string, error) {
	payload, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
