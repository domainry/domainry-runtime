package policy

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

const (
	RecordLifecycleEraseRetain    = "retain"
	RecordLifecycleEraseAnonymize = "anonymize"
	RecordLifecycleEraseDelete    = "delete"
)

// RecordSubjectIdentityFields returns fields that explicitly relate a record
// to a data subject. A Runtime user relation is inherently an identity link;
// custom identifiers must opt in through lifecycle_subject_identity.
func RecordSubjectIdentityFields(object definitionmodel.ObjectSchema) []definitionmodel.FieldSchema {
	fields := []definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		configured, hasConfiguration := recordLifecycleBool(field.Config, "lifecycle_subject_identity")
		if (hasConfiguration && configured) || (!hasConfiguration && strings.EqualFold(strings.TrimSpace(field.Type), "user")) {
			fields = append(fields, field)
		}
	}
	return fields
}

func RecordSubjectFileField(field definitionmodel.FieldSchema) bool {
	value, configured := recordLifecycleBool(field.Config, "lifecycle_subject_file")
	return configured && value
}

func RecordSubjectEraseMode(field definitionmodel.FieldSchema) string {
	mode, _ := field.Config["lifecycle_erase"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return RecordLifecycleEraseRetain
	}
	return mode
}

func RecordValidateSubjectLifecycle(object definitionmodel.ObjectSchema) error {
	for _, field := range object.Fields {
		if raw, ok := field.Config["lifecycle_subject_identity"]; ok {
			if _, valid := raw.(bool); !valid {
				return fmt.Errorf("%s.%s lifecycle_subject_identity must be boolean", object.Key, field.Key)
			}
		}
		if raw, ok := field.Config["lifecycle_subject_file"]; ok {
			if _, valid := raw.(bool); !valid {
				return fmt.Errorf("%s.%s lifecycle_subject_file must be boolean", object.Key, field.Key)
			}
		}
		if raw, ok := field.Config["lifecycle_subject_relation"]; ok {
			enabled, valid := raw.(bool)
			if !valid || enabled && (field.Type != "relation" || strings.TrimSpace(field.Validation.Target) == "") {
				return fmt.Errorf("%s.%s lifecycle_subject_relation requires a declared relation target", object.Key, field.Key)
			}
		}
		switch mode := RecordSubjectEraseMode(field); mode {
		case RecordLifecycleEraseRetain, RecordLifecycleEraseAnonymize, RecordLifecycleEraseDelete:
		default:
			return fmt.Errorf("%s.%s lifecycle_erase must be retain, anonymize, or delete", object.Key, field.Key)
		}
		if RecordSubjectFileField(field) && RecordSubjectEraseMode(field) == RecordLifecycleEraseAnonymize {
			return fmt.Errorf("%s.%s lifecycle file erase must be retain or delete", object.Key, field.Key)
		}
	}
	return nil
}

func recordLifecycleBool(config map[string]any, key string) (bool, bool) {
	if config == nil {
		return false, false
	}
	raw, ok := config[key]
	if !ok {
		return false, false
	}
	value, valid := raw.(bool)
	return value, valid
}

// RecordSubjectRelationTarget follows an explicitly owned business relation.
func RecordSubjectRelationTarget(field definitionmodel.FieldSchema) string {
	enabled, _ := recordLifecycleBool(field.Config, "lifecycle_subject_relation")
	if !enabled {
		return ""
	}
	return strings.TrimSpace(field.Validation.Target)
}
