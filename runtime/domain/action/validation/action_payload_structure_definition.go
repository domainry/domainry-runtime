package validation

import (
	"fmt"
	"regexp"
	"strings"

	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ActionPayloadFieldInvalidCode is the single definition-time error code for a
// malformed payload field tree. Params carry action, path and reason.
const ActionPayloadFieldInvalidCode = "backend.metadata.action_payload_field_invalid"

var actionPayloadFieldKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ActionPayloadFieldIssue is one definition-time payload contract violation.
// Path uses the metadata notation payload_fields[1].fields[0].
type ActionPayloadFieldIssue struct {
	Path   string
	Reason string
}

// ActionValidatePayloadFieldStructure validates the complete payload field tree
// of one Action: key identity and uniqueness per level, object/scalar shape,
// nesting depth, leaf count, repeated bounds, select options, relation targets
// and source lineage against the supplied objects (nil objects skips lineage
// existence checks, lineage pairing is always enforced).
func ActionValidatePayloadFieldStructure(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []ActionPayloadFieldIssue {
	walker := &actionPayloadStructureWalker{
		objects:      actionObjectFields(objects),
		objectsKnown: objects != nil,
		allowedTypes: actionDefinitionStringSet(appschemacontract.ApplicationSchemaFieldTypes()),
	}
	walker.walk(action.PayloadFields, "payload_fields", 1)
	if walker.leaves > definitionmodel.ActionPayloadMaxLeafFields {
		walker.add("payload_fields", fmt.Sprintf("declares %d leaf fields; at most %d are allowed", walker.leaves, definitionmodel.ActionPayloadMaxLeafFields))
	}
	return walker.issues
}

// ActionPayloadFieldStructureIssues projects the structural issues into the
// shared definition issue contract used by authoring and metadata validation.
func ActionPayloadFieldStructureIssues(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	structural := ActionValidatePayloadFieldStructure(action, objects)
	issues := make([]appschemamodel.ApplicationDefinitionValidationIssue, 0, len(structural))
	for _, issue := range structural {
		issues = append(issues, actionDefinitionValidationIssue(ActionPayloadFieldInvalidCode, issue.Path, map[string]string{
			"action": strings.TrimSpace(action.Key), "path": issue.Path, "field": issue.Path, "reason": issue.Reason,
		}))
	}
	return issues
}

type actionPayloadStructureWalker struct {
	objects      map[string][]definitionmodel.FieldSchema
	objectsKnown bool
	allowedTypes map[string]bool
	leaves       int
	issues       []ActionPayloadFieldIssue
}

func (w *actionPayloadStructureWalker) add(path, reason string) {
	w.issues = append(w.issues, ActionPayloadFieldIssue{Path: path, Reason: reason})
}

func (w *actionPayloadStructureWalker) walk(fields []definitionmodel.ActionPayloadField, prefix string, depth int) {
	seen := map[string]bool{}
	for index, field := range fields {
		path := fmt.Sprintf("%s[%d]", prefix, index)
		key := strings.TrimSpace(field.Key)
		switch {
		case key == "":
			w.add(path+".key", "key is required")
		case !actionPayloadFieldKeyPattern.MatchString(key):
			w.add(path+".key", "key must be an identifier (letters, digits and underscores, not starting with a digit)")
		case seen[key]:
			w.add(path+".key", fmt.Sprintf("duplicate key %q at this level", key))
		}
		seen[key] = true
		fieldType := strings.TrimSpace(field.Type)
		if field.IsObject() {
			if len(field.Fields) == 0 {
				w.add(path+".fields", "object fields require at least one nested field")
			}
			if len(field.Options) > 0 {
				w.add(path+".options", "options are only valid for select fields")
			}
			if field.DefaultValue != nil {
				w.add(path+".default_value", "object fields do not accept a default value")
			}
		} else {
			w.leaves++
			if len(field.Fields) > 0 {
				w.add(path+".fields", "nested fields require type object")
			}
			if fieldType != "" && !w.allowedTypes[fieldType] {
				w.add(path+".type", fmt.Sprintf("unknown payload field type %q", fieldType))
			}
			if len(field.Options) > 0 && fieldType != "select" && fieldType != "multi_select" {
				w.add(path+".options", "options are only valid for select and multi_select fields")
			}
			if fieldType == "relation" && strings.TrimSpace(field.TargetObjectKey) == "" {
				w.add(path+".target_object_key", "relation fields require target_object_key")
			}
		}
		if field.TargetObjectKey != "" && fieldType != "relation" {
			w.add(path+".target_object_key", "target_object_key is only valid for relation fields")
		}
		if field.MinItems != nil || field.MaxItems != nil {
			if !field.Repeated {
				w.add(path+".min_items", "min_items and max_items require repeated")
			}
			minItems, maxItems := 0, definitionmodel.ActionPayloadMaxItems
			if field.MinItems != nil {
				minItems = *field.MinItems
			}
			if field.MaxItems != nil {
				maxItems = *field.MaxItems
			}
			if minItems < 0 {
				w.add(path+".min_items", "min_items must not be negative")
			}
			if maxItems > definitionmodel.ActionPayloadMaxItems {
				w.add(path+".max_items", fmt.Sprintf("max_items must not exceed %d", definitionmodel.ActionPayloadMaxItems))
			}
			if minItems > maxItems {
				w.add(path+".min_items", "min_items must not exceed max_items")
			}
		}
		w.validateLineage(path, field)
		if field.IsObject() && len(field.Fields) > 0 {
			if depth+1 > definitionmodel.ActionPayloadMaxDepth {
				w.add(path+".fields", fmt.Sprintf("nesting exceeds the maximum depth of %d", definitionmodel.ActionPayloadMaxDepth))
				continue
			}
			w.walk(field.Fields, path+".fields", depth+1)
		}
	}
}

func (w *actionPayloadStructureWalker) validateLineage(path string, field definitionmodel.ActionPayloadField) {
	sourceObjectKey, sourceFieldKey := strings.TrimSpace(field.SourceObjectKey), strings.TrimSpace(field.SourceFieldKey)
	if (sourceObjectKey == "") != (sourceFieldKey == "") {
		w.add(path+".source_object_key", "source_object_key and source_field_key must be declared together")
		return
	}
	if sourceObjectKey == "" || !w.objectsKnown {
		return
	}
	objectFields, exists := w.objects[sourceObjectKey]
	if !exists {
		w.add(path+".source_object_key", fmt.Sprintf("unknown object %q", sourceObjectKey))
		return
	}
	for _, objectField := range objectFields {
		if strings.TrimSpace(objectField.Key) == sourceFieldKey {
			return
		}
	}
	w.add(path+".source_field_key", fmt.Sprintf("unknown field %q on object %q", sourceFieldKey, sourceObjectKey))
}
