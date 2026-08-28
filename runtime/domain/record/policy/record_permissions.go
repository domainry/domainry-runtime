package policy

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func RecordCanAccess(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	if !principal.Known {
		return false
	}
	if allowed, handled := RecordSDKAllowsRecord(principal, object, "read", record); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "read")
}

func RecordApplyOwnerDefault(object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) {
	if data == nil {
		return
	}
	ownerField := RecordOwnerFieldKey(object)
	if ownerField != "" {
		currentOwner := strings.TrimSpace(fmt.Sprint(data[ownerField]))
		if currentOwner == "current_user" || (recordPolicyIsEmptyValue(data[ownerField]) && RecordOwnerAutoAssignCurrentUser(object, ownerField)) {
			data[ownerField] = principal.UserID
		}
		if strings.TrimSpace(fmt.Sprint(data[ownerField])) == "" {
			return
		}
	}
	departmentPathField := RecordOwnerDepartmentPathFieldKey(object)
	if departmentPathField != "" && strings.TrimSpace(principal.DepartmentPath) != "" {
		data[departmentPathField] = principal.DepartmentPath
	}
	departmentIDField := RecordOwnerDepartmentIDFieldKey(object)
	if departmentIDField != "" && strings.TrimSpace(principal.DepartmentID) != "" {
		data[departmentIDField] = principal.DepartmentID
	}
}

func RecordOwnerAutoAssignCurrentUser(object definitionmodel.ObjectSchema, ownerField string) bool {
	for _, field := range object.Fields {
		if field.Key != ownerField {
			continue
		}
		value, ok := field.Config["auto_assign_current_user"]
		if ok {
			enabled, valid := boolAny(value)
			if valid {
				return enabled
			}
		}
		return field.Required
	}
	return false
}

func RecordApplyFieldDefaults(object definitionmodel.ObjectSchema, data map[string]any) {
	if data == nil {
		return
	}
	for _, field := range object.Fields {
		if !recordPolicyIsEmptyValue(data[field.Key]) {
			continue
		}
		if field.Default != nil {
			data[field.Key] = field.Default
			continue
		}
		if field.DefaultValue != nil {
			data[field.Key] = field.DefaultValue
		}
	}
}

func RecordCanWriteScope(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) bool {
	if !principal.Known {
		return false
	}
	if allowed, handled := RecordSDKAllowsRecord(principal, object, "update", recordmodel.Record{Data: data}); handled {
		return allowed
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "update")
}

func RecordFilterReadable(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) recordmodel.Record {
	record.Data = RecordFilterReadableData(principal, object, record.Data)
	return record
}

func RecordFilterReadableData(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) map[string]any {
	filtered := map[string]any{}
	for _, field := range object.Fields {
		if allowed, masked, handled := RecordSDKReadableField(principal, object.Key, field.Key); handled {
			if !allowed {
				continue
			}
			if value, ok := data[field.Key]; ok {
				if masked {
					filtered[field.Key] = RecordMaskFieldValue(field, value)
				} else {
					filtered[field.Key] = value
				}
			}
			continue
		}
		if !RecordCanReadObjectFieldForPrincipal(principal, object, field) {
			continue
		}
		if value, ok := data[field.Key]; ok {
			if RecordFieldReadMaskedForPrincipal(principal, object.Key, field.Key) {
				filtered[field.Key] = RecordMaskFieldValue(field, value)
				continue
			}
			filtered[field.Key] = value
		}
	}
	return filtered
}

func RecordValidateWritableFields(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) error {
	for key := range data {
		field, found := recordObjectField(object, key)
		if found {
			if allowed, _, handled := RecordSDKWritableField(principal, object.Key, field.Key); handled {
				if allowed {
					continue
				}
				return &apperror.CodedError{Code: "backend.validation.field_not_writable", Params: map[string]string{"field": key, "role": principal.RoleKey}}
			}
		}
		if !found || !RecordCanWriteObjectFieldForPrincipal(principal, object, field) {
			return &apperror.CodedError{Code: "backend.validation.field_not_writable", Params: map[string]string{"field": key, "role": principal.RoleKey}}
		}
	}
	return nil
}

func RecordExportableFieldsForPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema) []definitionmodel.FieldSchema {
	fields := []definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		if allowed, _, handled := RecordSDKExportableField(principal, object.Key, field.Key); handled {
			if allowed {
				fields = append(fields, field)
			}
			continue
		}
		if RecordCanExportObjectFieldForPrincipal(principal, object, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

func RecordExportMaskedFieldKeysForPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema) []string {
	keys := []string{}
	for _, field := range object.Fields {
		if allowed, masked, handled := RecordSDKExportableField(principal, object.Key, field.Key); handled {
			if allowed && masked {
				keys = append(keys, field.Key)
			}
			continue
		}
		if RecordCanExportObjectFieldForPrincipal(principal, object, field) && RecordFieldExportMaskedForPrincipal(principal, object.Key, field.Key) {
			keys = append(keys, field.Key)
		}
	}
	return keys
}

func RecordExportFieldKeys(fields []definitionmodel.FieldSchema) []string {
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.Key)
	}
	return keys
}

func recordObjectField(object definitionmodel.ObjectSchema, key string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == key && field.DisabledAt == "" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func RecordMaskFieldValue(field definitionmodel.FieldSchema, value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return ""
	}
	if field.Type == "email" {
		parts := strings.SplitN(text, "@", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return "****@" + parts[1]
		}
	}
	runes := []rune(text)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

func recordPolicyIsEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func boolAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	}
	return false, false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
